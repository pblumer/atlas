# The Teams Phone Worker Type's operations. Shipped inside Atlas and run by the worker,
# one pwsh process per job; a model never authors any of it.
#
# The request arrives as one hashtable: the operation, its prepared values (already
# normalized and checked by the worker), the tenant and the app's certificate. The
# answer is one line of JSON on stdout, {"ok":true,"result":{...}} or
# {"ok":false,"error":"..."}; everything else this script says goes to stderr.
#
# Every cmdlet whose output is not wanted is assigned to $null. A PowerShell function
# returns everything its statements emit, so one stray object would become part of a
# result.
param([hashtable]$Request)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'

$CallQueueApplicationId = '11cd3e2e-fccb-42ad-ad00-878b93575e07'
$AutoAttendantApplicationId = 'ce933385-9390-45d1-9512-c8d228074e07'
# The two Microsoft endpoints. A request may name others, which only the worker's own
# tests do, against a local stand-in.
$GraphRoot = if ($Request.graphRoot) { [string]$Request.graphRoot } else { 'https://graph.microsoft.com/v1.0' }
$LoginRoot = if ($Request.loginRoot) { [string]$Request.loginRoot } else { 'https://login.microsoftonline.com' }

function Write-Log([string]$Message) {
    [Console]::Error.WriteLine("teamsphone: $Message")
}

# The app's certificate: from the PEM the worker was handed, or from the host's own
# store by thumbprint. A store certificate is looked up for the current user first and
# the machine second, which is where an operator who imported it by hand put it.
function Get-AppCertificate {
    if ($Request.certificate) {
        # The one-argument CreateFromPem reads the certificate and drops the key; given
        # the same text twice it reads the key from its PRIVATE KEY block.
        $pem = [string]$Request.certificate
        $cert = [System.Security.Cryptography.X509Certificates.X509Certificate2]::CreateFromPem($pem, $pem)
        if (-not $cert.HasPrivateKey) { throw 'the certificate carries no private key' }
        return $cert
    }
    $thumbprint = ([string]$Request.certificateThumbprint -replace '[^0-9A-Fa-f]', '').ToUpperInvariant()
    foreach ($location in 'CurrentUser', 'LocalMachine') {
        $found = @()
        try {
            $store = [System.Security.Cryptography.X509Certificates.X509Store]::new('My', $location)
            $store.Open('ReadOnly')
            try { $found = @($store.Certificates.Find('FindByThumbprint', $thumbprint, $false)) } finally { $store.Close() }
        } catch { continue }
        if ($found.Count -eq 0) { continue }
        if (-not $found[0].HasPrivateKey) { throw "the certificate $thumbprint in $location\My has no private key this account may use" }
        return $found[0]
    }
    throw "no certificate with the thumbprint $thumbprint in CurrentUser\My or LocalMachine\My of the account the worker runs as"
}

function Connect-Teams {
    Write-Log 'importing MicrosoftTeams'
    $null = Import-Module MicrosoftTeams -DisableNameChecking
    Write-Log 'signing in to Microsoft Teams'
    $null = Connect-MicrosoftTeams -ApplicationId $Request.clientId -TenantId $Request.tenantId -Certificate $script:Certificate
}

function ConvertTo-Base64Url([byte[]]$Bytes) {
    [Convert]::ToBase64String($Bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

# A Graph token for the same app, signed with the same certificate: a client assertion
# (RFC 7523) in place of a client secret, which is what the Graph modules would do and
# what saves the worker host from needing them.
function Get-GraphToken {
    if ($script:GraphToken) { return $script:GraphToken }
    $tokenUrl = "$LoginRoot/$([uri]::EscapeDataString([string]$Request.tenantId))/oauth2/v2.0/token"
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $header = [ordered]@{
        alg        = 'RS256'
        typ        = 'JWT'
        x5t        = ConvertTo-Base64Url $script:Certificate.GetCertHash()
        'x5t#S256' = ConvertTo-Base64Url $script:Certificate.GetCertHash([System.Security.Cryptography.HashAlgorithmName]::SHA256)
    }
    $claims = [ordered]@{
        aud = $tokenUrl; iss = [string]$Request.clientId; sub = [string]$Request.clientId
        jti = [guid]::NewGuid().ToString('N'); nbf = $now; iat = $now; exp = $now + 600
    }
    $utf8 = [System.Text.Encoding]::UTF8
    $unsigned = (ConvertTo-Base64Url $utf8.GetBytes(($header | ConvertTo-Json -Compress))) + '.' +
        (ConvertTo-Base64Url $utf8.GetBytes(($claims | ConvertTo-Json -Compress)))
    $rsa = [System.Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($script:Certificate)
    if (-not $rsa) { throw 'the certificate has no RSA key; Microsoft Entra signs app sign-ins with RSA' }
    $signature = $rsa.SignData($utf8.GetBytes($unsigned), [System.Security.Cryptography.HashAlgorithmName]::SHA256,
        [System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
    $body = @{
        grant_type            = 'client_credentials'
        client_id             = [string]$Request.clientId
        client_assertion_type = 'urn:ietf:params:oauth:client-assertion-type:jwt-bearer'
        client_assertion      = $unsigned + '.' + (ConvertTo-Base64Url $signature)
        scope                 = 'https://graph.microsoft.com/.default'
    }
    $response = Invoke-RestMethod -Method Post -Uri $tokenUrl -Body $body -ContentType 'application/x-www-form-urlencoded'
    $script:GraphToken = [string]$response.access_token
    return $script:GraphToken
}

# One Graph call. Found is false for a 404, which every caller here reads as "not
# there" rather than as a failure.
function Invoke-Graph([string]$Method, [string]$Path) {
    $headers = @{ Authorization = "Bearer $(Get-GraphToken)" }
    try {
        $body = Invoke-RestMethod -Method $Method -Uri ($GraphRoot + $Path) -Headers $headers
        return @{ Found = $true; Body = $body }
    } catch {
        $status = 0
        if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
        if ($status -eq 404) { return @{ Found = $false; Body = $null } }
        throw "Microsoft Graph $Method $Path failed: $($_.Exception.Message)"
    }
}

function Get-ApplicationInstance([string]$Identity) {
    try { return Get-CsOnlineApplicationInstance -Identity $Identity -ErrorAction Stop } catch { return $null }
}

function Get-Association([string]$AccountId) {
    try { $a = Get-CsOnlineApplicationInstanceAssociation -Identity $AccountId -ErrorAction Stop } catch { return $null }
    if ($a -and $a.ConfigurationId) { return $a }
    return $null
}

# The resource account a voice app is to be associated with must be one Teams already
# shows, and one created for that kind of voice app. Teams shows a new account only after
# a synchronization, so this waits for it — up to accountWaitSeconds, polling — and then
# fails the job before anything is created, so the retry finds nothing half-made.
function Assert-Account([string]$AccountId, [string]$ApplicationId, [string]$Label) {
    $wait = if ($null -ne $Request.accountWaitSeconds) { [int]$Request.accountWaitSeconds } else { 120 }
    $deadline = [DateTime]::UtcNow.AddSeconds($wait)
    while (-not ($ra = Get-ApplicationInstance $AccountId)) {
        if ([DateTime]::UtcNow -ge $deadline) {
            throw "the resource account $AccountId is not visible to Teams yet (Teams synchronizes new accounts after a delay; waited $wait s); retry the task later"
        }
        Write-Log "resource account $AccountId not visible to Teams yet; waiting"
        Start-Sleep -Seconds 15
    }
    if ($ra.ApplicationId -and ([string]$ra.ApplicationId) -ne $ApplicationId) {
        throw "the resource account $($ra.UserPrincipalName) was created for another kind of voice app and cannot carry a $Label"
    }
}

function Resolve-Users {
    $ids = [System.Collections.Generic.List[string]]::new()
    $notFound = [System.Collections.Generic.List[string]]::new()
    foreach ($u in @($Request.args.users)) {
        $user = $null
        try { $user = Get-CsOnlineUser -Identity $u -ErrorAction Stop } catch { Write-Log "user $u not resolved: $($_.Exception.Message)" }
        if ($user) { $ids.Add([string]$user.Identity) } else { $notFound.Add([string]$u) }
    }
    return [ordered]@{ ids = @($ids); notFound = @($notFound) }
}

function Find-ResourceAccount([string]$Upn, [string]$DisplayName, [string]$ApplicationId) {
    $ra = Get-ApplicationInstance $Upn
    if ($ra) {
        if ($ra.ApplicationId -and ([string]$ra.ApplicationId) -ne $ApplicationId) {
            throw "$Upn is a resource account for another kind of voice app"
        }
        return @{ ObjectId = [string]$ra.ObjectId; Source = 'teams' }
    }
    # Teams shows a new account only after a synchronization; Entra holds it at once.
    $user = Invoke-Graph 'GET' "/users/$([uri]::EscapeDataString($Upn))?`$select=id,displayName,accountEnabled"
    if (-not $user.Found) { return $null }
    if ($user.Body.accountEnabled -and $user.Body.displayName -ne $DisplayName) {
        throw "$Upn belongs to an enabled user account ('$($user.Body.displayName)'), not to a resource account; choose another user principal name"
    }
    return @{ ObjectId = [string]$user.Body.id; Source = 'entra' }
}

function Set-ResourceAccount {
    $upn = [string]$Request.args.userPrincipalName
    $displayName = [string]$Request.args.displayName
    $appId = if ($Request.args.accountType -eq 'call-queue') { $CallQueueApplicationId } else { $AutoAttendantApplicationId }
    $result = { param($id, $created, $source) [ordered]@{ objectId = $id; userPrincipalName = $upn; created = $created; source = $source } }
    $found = Find-ResourceAccount $upn $displayName $appId
    if ($found) { return & $result $found.ObjectId $false $found.Source }
    try {
        $ra = New-CsOnlineApplicationInstance -UserPrincipalName $upn -DisplayName $displayName -ApplicationId $appId
    } catch {
        # Created between the lookup and here — by a parallel run, or by a first run
        # whose answer was lost. Look again before calling it a failure.
        $found = Find-ResourceAccount $upn $displayName $appId
        if (-not $found) { throw }
        return & $result $found.ObjectId $false $found.Source
    }
    Write-Log "resource account $upn created"
    return & $result ([string]$ra.ObjectId) $true 'created'
}

# The agents a model names are the queue's agents, so an existing queue is brought to
# the list rather than returned as it is: a process run again after a change of staff
# means the new list. Only agents named directly are compared — those a distribution list
# or a channel brings are Teams' to keep — ids without regard to case, and the order
# only where serial routing rings in it. A list that would take every agent away is
# refused: a Resolve users that found nobody reads exactly like a queue nobody staffs.
# The worker passes agents only when the model named them; not named is "leave them".
function Sync-QueueAgents($Queue) {
    $a = $Request.args
    if (-not $a.Contains('agents')) { return $false }
    $serial = [string]$a.routingMethod -eq 'serial'
    $key = {
        param($List)
        $k = @(foreach ($u in $List) { ([string]$u).ToLowerInvariant() })
        if (-not $serial) { $k = @($k | Sort-Object) }
        $k -join ','
    }
    $want = @(foreach ($u in $a.agents) { [string]$u })
    $have = @(foreach ($u in $Queue.Users) { [string]$u })
    if ((& $key $want) -eq (& $key $have)) { return $false }
    if ($want.Count -eq 0) {
        throw "the empty list of agents would remove every agent from the call queue $($Queue.Name), which has $($have.Count); an ensure never empties a queue, because a Resolve users that found nobody reads the same: name at least one agent, or leave agents out to keep the ones it has"
    }
    $null = Set-CsCallQueue -Identity $Queue.Identity -Users $want
    Write-Log "call queue $($Queue.Name): agents set to $($want.Count)"
    return $true
}

function Set-CallQueue {
    $a = $Request.args
    $accountId = [string]$a.resourceAccountId
    $name = [string]$a.name
    Assert-Account $accountId $CallQueueApplicationId 'call queue'
    $assoc = Get-Association $accountId
    if ($assoc) {
        if ($assoc.ConfigurationType -and ([string]$assoc.ConfigurationType) -ne 'CallQueue') {
            throw "the resource account $accountId is already associated with an auto attendant"
        }
        $queue = Get-CsCallQueue -Identity $assoc.ConfigurationId
        $changed = Sync-QueueAgents $queue
        return [ordered]@{ id = [string]$queue.Identity; name = [string]$queue.Name; created = $false; agentsChanged = $changed }
    }
    $created = $false
    $changed = $false
    $queue = Get-CsCallQueue -NameFilter $name | Where-Object { $_.Name -eq $name } | Select-Object -First 1
    if ($queue) {
        # Before the association: a refused list leaves the queue as unattached as it was.
        $changed = Sync-QueueAgents $queue
    } else {
        $routing = @{ 'attendant' = 'Attendant'; 'serial' = 'Serial'; 'round-robin' = 'RoundRobin'; 'longest-idle' = 'LongestIdle' }
        $params = @{
            Name                  = $name
            UseDefaultMusicOnHold = $true
            RoutingMethod         = $routing[[string]$a.routingMethod]
            PresenceBasedRouting  = [bool]$a.presenceBasedRouting
            ConferenceMode        = [bool]$a.conferenceMode
            AllowOptOut           = [bool]$a.allowOptOut
        }
        if ($a.languageId) { $params.LanguageId = [string]$a.languageId }
        # Agents a model did not name arrive as no value at all, and @($null) is one
        # element, not none; foreach takes neither a null nor an empty list for an agent.
        $agents = @(foreach ($u in $a.agents) { [string]$u })
        if ($agents.Count -gt 0) { $params.Users = $agents }
        if (@($a.authorizedUsers).Count -gt 0) { $params.AuthorizedUsers = @($a.authorizedUsers) }
        $queue = New-CsCallQueue @params
        $created = $true
        Write-Log "call queue $name created"
    }
    $null = New-CsOnlineApplicationInstanceAssociation -Identities @($accountId) -ConfigurationId $queue.Identity -ConfigurationType CallQueue
    return [ordered]@{ id = [string]$queue.Identity; name = [string]$queue.Name; created = $created; agentsChanged = $changed }
}

function ConvertTo-TeamsDate([string]$IsoDate) {
    $inv = [System.Globalization.CultureInfo]::InvariantCulture
    [datetime]::ParseExact($IsoDate, 'yyyy-MM-dd', $inv).ToString('d/M/yyyy', $inv)
}

function Set-AutoAttendant {
    $a = $Request.args
    $accountId = [string]$a.resourceAccountId
    $name = [string]$a.name
    Assert-Account $accountId $AutoAttendantApplicationId 'auto attendant'
    $assoc = Get-Association $accountId
    if ($assoc) {
        if ($assoc.ConfigurationType -and ([string]$assoc.ConfigurationType) -ne 'AutoAttendant') {
            throw "the resource account $accountId is already associated with a call queue"
        }
        $attendant = Get-CsAutoAttendant -Identity $assoc.ConfigurationId
        return [ordered]@{ id = [string]$attendant.Identity; name = [string]$attendant.Name; created = $false }
    }
    $created = $false
    $attendant = Get-CsAutoAttendant -NameFilter $name | Where-Object { $_.Name -eq $name } | Select-Object -First 1
    if (-not $attendant) {
        $target = New-CsAutoAttendantCallableEntity -Identity ([string]$a.transferTarget) -Type ApplicationEndpoint
        $option = New-CsAutoAttendantMenuOption -Action TransferCallToTarget -DtmfResponse Automatic -CallTarget $target
        $menu = New-CsAutoAttendantMenu -Name "$name menu" -MenuOptions @($option)
        $flow = New-CsAutoAttendantCallFlow -Name "$name call flow" -Menu $menu
        $flows = [System.Collections.Generic.List[object]]::new()
        $handling = [System.Collections.Generic.List[object]]::new()
        foreach ($h in @($a.holidays)) {
            if (-not $h) { continue }
            $range = New-CsOnlineDateTimeRange -Start (ConvertTo-TeamsDate $h.start) -End (ConvertTo-TeamsDate $h.end)
            $schedule = New-CsOnlineSchedule -Name ([string]$h.name) -FixedSchedule -DateTimeRanges @($range)
            $disconnect = New-CsAutoAttendantMenuOption -Action DisconnectCall -DtmfResponse Automatic
            $holidayMenu = New-CsAutoAttendantMenu -Name "$($h.name) menu" -MenuOptions @($disconnect)
            $holidayFlow = New-CsAutoAttendantCallFlow -Name "$($h.name) call flow" -Menu $holidayMenu
            $flows.Add($holidayFlow)
            $handling.Add((New-CsAutoAttendantCallHandlingAssociation -Type Holiday -ScheduleId $schedule.Id -CallFlowId $holidayFlow.Id))
        }
        $params = @{
            Name            = $name
            LanguageId      = [string]$a.languageId
            TimeZoneId      = [string]$a.timeZone
            DefaultCallFlow = $flow
        }
        if ($flows.Count -gt 0) {
            $params.CallFlows = @($flows)
            $params.CallHandlingAssociations = @($handling)
        }
        if (@($a.authorizedUsers).Count -gt 0) { $params.AuthorizedUsers = @($a.authorizedUsers) }
        $attendant = New-CsAutoAttendant @params
        $created = $true
        Write-Log "auto attendant $name created"
    }
    $null = New-CsOnlineApplicationInstanceAssociation -Identities @($accountId) -ConfigurationId $attendant.Identity -ConfigurationType AutoAttendant
    return [ordered]@{ id = [string]$attendant.Identity; name = [string]$attendant.Name; created = $created }
}

# Removes the voice app a resource account is associated with: the association first,
# then the voice app. A run that failed between the two leaves a voice app no account
# points at, which the optional name finds again.
function Remove-VoiceApp([string]$Kind) {
    $a = $Request.args
    $accountId = [string]$a.resourceAccountId
    $name = [string]$a.name
    $get = if ($Kind -eq 'CallQueue') { 'Get-CsCallQueue' } else { 'Get-CsAutoAttendant' }
    $remove = if ($Kind -eq 'CallQueue') { 'Remove-CsCallQueue' } else { 'Remove-CsAutoAttendant' }
    $id = $null
    $assoc = Get-Association $accountId
    if ($assoc) {
        if ($assoc.ConfigurationType -and ([string]$assoc.ConfigurationType) -ne $Kind) {
            throw "the resource account $accountId is associated with another kind of voice app than a $Kind"
        }
        $id = [string]$assoc.ConfigurationId
        $null = Remove-CsOnlineApplicationInstanceAssociation -Identities @($accountId)
    } elseif ($name) {
        $orphan = & $get -NameFilter $name | Where-Object { $_.Name -eq $name -and @($_.ApplicationInstances).Count -eq 0 } | Select-Object -First 1
        if ($orphan) { $id = [string]$orphan.Identity }
    }
    if (-not $id) { return [ordered]@{ removed = $false; id = '' } }
    $null = & $remove -Identity $id
    Write-Log "$Kind $id removed"
    return [ordered]@{ removed = $true; id = $id }
}

# A resource account is an Entra user: Teams has no cmdlet that deletes one. So the
# association goes through Teams and the account through Graph.
function Remove-ResourceAccount {
    $accountId = [string]$Request.args.resourceAccountId
    if (Get-Association $accountId) {
        $null = Remove-CsOnlineApplicationInstanceAssociation -Identities @($accountId)
    }
    $deleted = Invoke-Graph 'DELETE' "/users/$([uri]::EscapeDataString($accountId))"
    return [ordered]@{ removed = [bool]$deleted.Found; objectId = $accountId }
}

$envelope = $null
try {
    $script:Certificate = Get-AppCertificate
    Connect-Teams
    $result = switch ([string]$Request.operation) {
        'resolve-users' { Resolve-Users }
        'ensure-resource-account' { Set-ResourceAccount }
        'ensure-call-queue' { Set-CallQueue }
        'ensure-auto-attendant' { Set-AutoAttendant }
        'remove-call-queue' { Remove-VoiceApp 'CallQueue' }
        'remove-auto-attendant' { Remove-VoiceApp 'AutoAttendant' }
        'remove-resource-account' { Remove-ResourceAccount }
        default { throw "unknown operation $($Request.operation)" }
    }
    $envelope = [ordered]@{ ok = $true; result = $result }
} catch {
    $envelope = [ordered]@{ ok = $false; error = $_.Exception.Message }
} finally {
    try { $null = Disconnect-MicrosoftTeams -ErrorAction SilentlyContinue } catch { }
}
# ASCII only, so the answer reads the same whatever code page the host's console has.
[Console]::Out.WriteLine(($envelope | ConvertTo-Json -Depth 8 -Compress -EscapeHandling EscapeNonAscii))
