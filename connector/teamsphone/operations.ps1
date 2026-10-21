# The Teams Phone Worker Type's operations. Shipped inside Atlas and run by the worker,
# one pwsh process per job; a model never authors any of it.
#
# The request arrives as one hashtable: the operation, its prepared values (already
# normalized and checked by the worker), the names among them an existing object is
# brought to (reconcile), the tenant and the app's certificate. The answer is one line of
# JSON on stdout, {"ok":true,"result":{...}} or {"ok":false,"error":"...","code":"..."},
# the code only for a refusal a process may have a path for; everything else this script
# says goes to stderr.
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
$RoutingNames = @{ 'attendant' = 'Attendant'; 'serial' = 'Serial'; 'round-robin' = 'RoundRobin'; 'longest-idle' = 'LongestIdle' }
$NumberTypes = @{ 'direct-routing' = 'DirectRouting'; 'calling-plan' = 'CallingPlan'; 'operator-connect' = 'OperatorConnect' }
$Capabilities = @{ 'user' = 'UserAssignment'; 'resource-account' = 'VoiceApplicationAssignment' }
# The two Microsoft endpoints. A request may name others, which only the worker's own
# tests do, against a local stand-in.
$GraphRoot = if ($Request.graphRoot) { [string]$Request.graphRoot } else { 'https://graph.microsoft.com/v1.0' }
$LoginRoot = if ($Request.loginRoot) { [string]$Request.loginRoot } else { 'https://login.microsoftonline.com' }

function Write-Log([string]$Message) {
    [Console]::Error.WriteLine("teamsphone: $Message")
}

# A refusal a process may have a path for carries one of the codes the worker knows. The
# worker throws it as a BPMN error when the task's process catches that code, and fails
# the job as before when it does not, which keeps the retries a fault is owed.
function Stop-Refused([string]$Code, [string]$Message) {
    $e = [System.InvalidOperationException]::new($Message)
    $e.Data['atlasCode'] = $Code
    throw $e
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
            Stop-Refused 'TEAMSPHONE_ACCOUNT_NOT_VISIBLE' "the resource account $AccountId is not visible to Teams yet (Teams synchronizes new accounts after a delay; waited $wait s); retry the task later"
        }
        Write-Log "resource account $AccountId not visible to Teams yet; waiting"
        Start-Sleep -Seconds 15
    }
    if ($ra.ApplicationId -and ([string]$ra.ApplicationId) -ne $ApplicationId) {
        Stop-Refused 'TEAMSPHONE_ACCOUNT_TYPE_MISMATCH' "the resource account $($ra.UserPrincipalName) was created for another kind of voice app and cannot carry a $Label"
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
            Stop-Refused 'TEAMSPHONE_ACCOUNT_TYPE_MISMATCH' "$Upn is a resource account for another kind of voice app"
        }
        return @{ ObjectId = [string]$ra.ObjectId; Source = 'teams' }
    }
    # Teams shows a new account only after a synchronization; Entra holds it at once.
    $user = Invoke-Graph 'GET' "/users/$([uri]::EscapeDataString($Upn))?`$select=id,displayName,accountEnabled"
    if (-not $user.Found) { return $null }
    if ($user.Body.accountEnabled -and $user.Body.displayName -ne $DisplayName) {
        Stop-Refused 'TEAMSPHONE_UPN_TAKEN' "$Upn belongs to an enabled user account ('$($user.Body.displayName)'), not to a resource account; choose another user principal name"
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

# The key two lists of ids are compared by: the ids in lower case, sorted unless the
# order is the routing. Ordinal, so it agrees with the worker's mock.
function Get-ListKey($List, [bool]$Ordered) {
    [string[]]$k = @(foreach ($u in $List) { ([string]$u).ToLowerInvariant() })
    if (-not $Ordered) { [Array]::Sort($k, [StringComparer]::Ordinal) }
    $k -join ','
}

# What a model names is what the queue is, so an existing queue is brought to it rather
# than returned as it is: a process run again after a change of staff or of routing means
# the new values. The worker lists in reconcile what the model named; anything else — a
# setting left out, a list not named — stays as the queue has it. Only agents named
# directly are compared — those a distribution list or a channel brings are Teams' to
# keep — ids without regard to case, and the agents' order only where the queue rings
# serially once this run is done. A list that would be emptied is refused before anything
# changes: a Resolve users that found nobody reads exactly like a queue nobody staffs.
# Every change goes in one Set-CsCallQueue; the answer is the names changed, sorted.
function Sync-CallQueue($Queue) {
    $a = $Request.args
    $named = @($Request.reconcile)
    $set = @{}
    $changed = [System.Collections.Generic.List[string]]::new()
    $routing = [string]$Queue.RoutingMethod
    if ($named -contains 'routingMethod') {
        $routing = $RoutingNames[[string]$a.routingMethod]
        if ([string]$Queue.RoutingMethod -ne $routing) { $set.RoutingMethod = $routing; $changed.Add('routingMethod') }
    }
    foreach ($s in @(
            @{ Name = 'presenceBasedRouting'; Param = 'PresenceBasedRouting' },
            @{ Name = 'conferenceMode'; Param = 'ConferenceMode' },
            @{ Name = 'allowOptOut'; Param = 'AllowOptOut' })) {
        if ($named -contains $s.Name -and [bool]$Queue.($s.Param) -ne [bool]$a[$s.Name]) {
            $set[$s.Param] = [bool]$a[$s.Name]
            $changed.Add($s.Name)
        }
    }
    foreach ($l in @(
            @{ Name = 'agents'; Param = 'Users'; Label = 'agents'; Ordered = ($routing -eq 'Serial') },
            @{ Name = 'authorizedUsers'; Param = 'AuthorizedUsers'; Label = 'authorized users'; Ordered = $false })) {
        if ($named -notcontains $l.Name) { continue }
        $want = @(foreach ($u in $a[$l.Name]) { [string]$u })
        $have = @(foreach ($u in $Queue.($l.Param)) { [string]$u })
        if ((Get-ListKey $want $l.Ordered) -eq (Get-ListKey $have $l.Ordered)) { continue }
        if ($want.Count -eq 0) {
            Stop-Refused 'TEAMSPHONE_WOULD_EMPTY_LIST' "the empty list of $($l.Name) would remove every one of the $($have.Count) $($l.Label) of the call queue $($Queue.Name); an ensure never empties a list, because a Resolve users that found nobody reads the same: name at least one, or leave $($l.Name) out to keep the ones it has"
        }
        $set[$l.Param] = $want
        $changed.Add($l.Name)
    }
    if ($set.Count -gt 0) {
        $null = Set-CsCallQueue -Identity $Queue.Identity @set
        Write-Log "call queue $($Queue.Name): $($changed -join ', ') set"
    }
    [string[]]$sorted = $changed.ToArray()
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    return $sorted
}

function Set-CallQueue {
    $a = $Request.args
    $accountId = [string]$a.resourceAccountId
    $name = [string]$a.name
    Assert-Account $accountId $CallQueueApplicationId 'call queue'
    $assoc = Get-Association $accountId
    if ($assoc) {
        if ($assoc.ConfigurationType -and ([string]$assoc.ConfigurationType) -ne 'CallQueue') {
            Stop-Refused 'TEAMSPHONE_ACCOUNT_IN_USE' "the resource account $accountId is already associated with an auto attendant"
        }
        $queue = Get-CsCallQueue -Identity $assoc.ConfigurationId
        $changed = @(Sync-CallQueue $queue)
        return [ordered]@{ id = [string]$queue.Identity; name = [string]$queue.Name; created = $false; changed = $changed; agentsChanged = ($changed -contains 'agents') }
    }
    $created = $false
    $changed = @()
    $queue = Get-CsCallQueue -NameFilter $name | Where-Object { $_.Name -eq $name } | Select-Object -First 1
    if ($queue) {
        # Before the association: a refused request leaves the queue as unattached as it was.
        $changed = @(Sync-CallQueue $queue)
    } else {
        $params = @{
            Name                  = $name
            UseDefaultMusicOnHold = $true
            RoutingMethod         = $RoutingNames[[string]$a.routingMethod]
            PresenceBasedRouting  = [bool]$a.presenceBasedRouting
            ConferenceMode        = [bool]$a.conferenceMode
            AllowOptOut           = [bool]$a.allowOptOut
        }
        if ($a.languageId) { $params.LanguageId = [string]$a.languageId }
        # A list a model did not name arrives as no value at all, and @($null) is one
        # element, not none; foreach takes neither a null nor an empty list for an entry.
        $agents = @(foreach ($u in $a.agents) { [string]$u })
        if ($agents.Count -gt 0) { $params.Users = $agents }
        $owners = @(foreach ($u in $a.authorizedUsers) { [string]$u })
        if ($owners.Count -gt 0) { $params.AuthorizedUsers = $owners }
        $queue = New-CsCallQueue @params
        $created = $true
        Write-Log "call queue $name created"
    }
    $null = New-CsOnlineApplicationInstanceAssociation -Identities @($accountId) -ConfigurationId $queue.Identity -ConfigurationType CallQueue
    # agentsChanged is what 0.16.0 answered; models written against it keep it.
    return [ordered]@{ id = [string]$queue.Identity; name = [string]$queue.Name; created = $created; changed = $changed; agentsChanged = ($changed -contains 'agents') }
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
            Stop-Refused 'TEAMSPHONE_ACCOUNT_IN_USE' "the resource account $accountId is already associated with a call queue"
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
    Remove-VoiceAppOf $Kind ([string]$Request.args.resourceAccountId) ([string]$Request.args.name)
}

function Remove-VoiceAppOf([string]$Kind, [string]$AccountId, [string]$Name) {
    $accountId = $AccountId
    $name = $Name
    $get = if ($Kind -eq 'CallQueue') { 'Get-CsCallQueue' } else { 'Get-CsAutoAttendant' }
    $remove = if ($Kind -eq 'CallQueue') { 'Remove-CsCallQueue' } else { 'Remove-CsAutoAttendant' }
    $id = $null
    $assoc = if ($accountId) { Get-Association $accountId } else { $null }
    if ($assoc) {
        if ($assoc.ConfigurationType -and ([string]$assoc.ConfigurationType) -ne $Kind) {
            $label = if ($Kind -eq 'CallQueue') { 'a call queue' } else { 'an auto attendant' }
            Stop-Refused 'TEAMSPHONE_ACCOUNT_IN_USE' "the resource account $accountId is associated with another kind of voice app than $label"
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

# Two spellings of one phone number compare equal: Teams answers with a leading +
# whichever was written.
function Get-NumberKey([string]$Number) { $Number.TrimStart('+').ToLowerInvariant() }

# Gives a user or resource account the number named. Nothing to do when the holder has
# it already; refused when somebody else holds it, or when the tenant lacks a Calling
# Plan or Operator Connect number — a Direct Routing number is the operator's carrier's
# and is in no inventory until it is assigned. The holder's primary number, if it has
# another, is removed first: Microsoft does not say that an assignment replaces one, and
# a retry after a failure between the two finds the holder without a number and simply
# assigns.
function Set-PhoneNumber {
    $a = $Request.args
    $identity = [string]$a.identity
    $number = [string]$a.phoneNumber
    $type = $NumberTypes[[string]$a.numberType]
    $result = { param($assigned, $previous) [ordered]@{ phoneNumber = $number; identity = $identity; assigned = $assigned; previousNumber = $previous } }
    $mine = @(Get-CsPhoneNumberAssignment -AssignedPstnTargetId $identity)
    if (@($mine | Where-Object { (Get-NumberKey $_.TelephoneNumber) -eq (Get-NumberKey $number) }).Count -gt 0) {
        return & $result $false ''
    }
    $entry = @(Get-CsPhoneNumberAssignment -TelephoneNumber $number) | Select-Object -First 1
    if ($entry -and $entry.AssignedPstnTargetId) {
        Stop-Refused 'TEAMSPHONE_NUMBER_IN_USE' "the phone number $number is assigned to another user or resource account; remove it there first"
    }
    if (-not $entry -and $type -ne 'DirectRouting') {
        Stop-Refused 'TEAMSPHONE_NUMBER_NOT_FOUND' "the phone number $number is not one of the tenant's $($a.numberType) numbers; acquire or port it first"
    }
    $previous = ''
    foreach ($old in @($mine | Where-Object { -not $_.AssignmentCategory -or [string]$_.AssignmentCategory -eq 'Primary' })) {
        $null = Remove-CsPhoneNumberAssignment -Identity $identity -TelephoneNumber $old.TelephoneNumber -NumberType $old.NumberType
        $previous = [string]$old.TelephoneNumber
        Write-Log "phone number $previous removed from $identity"
    }
    $params = @{ Identity = $identity; TelephoneNumber = $number; NumberType = $type }
    if ($a.locationId) { $params.LocationId = [string]$a.locationId }
    $null = Set-CsPhoneNumberAssignment @params
    Write-Log "phone number $number assigned to $identity"
    return & $result $true $previous
}

# Takes the number named — or, with none named, every number — away from its holder and
# answers which it removed, sorted; a number the holder does not have is nothing to remove.
function Remove-PhoneNumbers([string]$Identity, [string]$Number) {
    $mine = @(Get-CsPhoneNumberAssignment -AssignedPstnTargetId $Identity)
    if ($Number) {
        $key = Get-NumberKey $Number
        $mine = @($mine | Where-Object { (Get-NumberKey $_.TelephoneNumber) -eq $key })
    }
    $removed = [System.Collections.Generic.List[string]]::new()
    foreach ($n in $mine) {
        $null = Remove-CsPhoneNumberAssignment -Identity $Identity -TelephoneNumber $n.TelephoneNumber -NumberType $n.NumberType
        $removed.Add([string]$n.TelephoneNumber)
        Write-Log "phone number $($n.TelephoneNumber) removed from $Identity"
    }
    [string[]]$sorted = $removed.ToArray()
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    return $sorted
}

function Remove-PhoneNumber {
    $a = $Request.args
    $removed = @(Remove-PhoneNumbers ([string]$a.identity) ([string]$a.phoneNumber))
    return [ordered]@{ removed = ($removed.Count -gt 0); phoneNumbers = $removed }
}

# The numbers in the tenant's inventory nobody holds that Teams would give to the kind of
# holder named, sorted, no more than max. Teams filters by status, activation, type,
# capability and prefix; a number blocked for assignment is dropped here, because Teams
# lists it as unassigned and then refuses to give it out. One more than max is asked
# for, so truncated says whether Teams had more to offer. A Direct Routing number is in
# the inventory only once it has been uploaded to it.
function Get-FreePhoneNumbers {
    $a = $Request.args
    $most = [int]$a.max
    $params = @{
        PstnAssignmentStatus = 'Unassigned'; ActivationState = 'Activated'; Top = $most + 1
        NumberType = $NumberTypes[[string]$a.numberType]; CapabilitiesContain = $Capabilities[[string]$a.assignableTo]
    }
    if ($a.numberPrefix) { $params.TelephoneNumberStartsWith = [string]$a.numberPrefix }
    $rows = @(Get-CsPhoneNumberAssignment @params)
    [object[]]$free = @(foreach ($r in $rows) {
        $blocked = [string]$r.AssignmentBlockedState
        if ($blocked -and $blocked -ne 'NotBlocked') { continue }
        $r
    })
    [string[]]$keys = @(foreach ($r in $free) { [string]$r.TelephoneNumber })
    [Array]::Sort($keys, $free, [StringComparer]::Ordinal)
    $numbers = [System.Collections.Generic.List[string]]::new()
    $entries = [System.Collections.Generic.List[object]]::new()
    for ($i = 0; $i -lt [Math]::Min($free.Count, $most); $i++) {
        $number = [string]$free[$i].TelephoneNumber
        $numbers.Add($number)
        $entries.Add([ordered]@{ phoneNumber = $number; numberType = [string]$a.numberType; city = [string]$free[$i].City; isoCountryCode = [string]$free[$i].IsoCountryCode })
    }
    return [ordered]@{ numbers = @($numbers); entries = @($entries); count = $numbers.Count; truncated = ($rows.Count -gt $most) }
}

# A resource account is an Entra user: Teams has no cmdlet that deletes one. So the
# association goes through Teams and the account through Graph. The leaver path is one
# task: the account's phone numbers are released first — a number left on a deleted
# account is one nobody can give out until Teams notices — then the association goes,
# then the account. An account Entra no longer holds is answered removed=false before
# Teams is asked anything, so a repeat does not depend on what Teams says about an
# account that is gone.
function Remove-ResourceAccount {
    Remove-Account ([string]$Request.args.resourceAccountId)
}

function Remove-Account([string]$AccountId) {
    $accountId = $AccountId
    $path = "/users/$([uri]::EscapeDataString($accountId))"
    if (-not (Invoke-Graph 'GET' ($path + '?$select=id')).Found) {
        return [ordered]@{ removed = $false; objectId = $accountId; phoneNumbers = @() }
    }
    $released = @(Remove-PhoneNumbers $accountId '')
    if (Get-Association $accountId) {
        $null = Remove-CsOnlineApplicationInstanceAssociation -Identities @($accountId)
    }
    $deleted = Invoke-Graph 'DELETE' $path
    return [ordered]@{ removed = [bool]$deleted.Found; objectId = $accountId; phoneNumbers = $released }
}

# The ids of a list, sorted as the worker's mock sorts them.
function Get-SortedIds($List) {
    [string[]]$sorted = @(foreach ($i in $List) { [string]$i })
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    return $sorted
}

# A whole hotline in one task, in the one order that never leaves a caller ringing into
# nothing: the auto attendants first, then the queues they hand calls to, then each
# account's numbers and the account. Before anything changes, every voice app the
# accounts carry is checked for an account the hotline does not name: such a voice app
# answers for somebody else too, and the whole request is refused. names finds a voice
# app an earlier run left without its account.
function Remove-Hotline {
    $a = $Request.args
    $ids = @(foreach ($u in $a.resourceAccountIds) { [string]$u })
    $names = @(foreach ($n in $a.names) { [string]$n })
    $getters = @{ 'AutoAttendant' = 'Get-CsAutoAttendant'; 'CallQueue' = 'Get-CsCallQueue' }
    foreach ($id in $ids) {
        $assoc = Get-Association $id
        if (-not $assoc) { continue }
        $app = & $getters[[string]$assoc.ConfigurationType] -Identity $assoc.ConfigurationId
        foreach ($other in @($app.ApplicationInstances)) {
            if ($other -and $ids -notcontains [string]$other) {
                Stop-Refused 'TEAMSPHONE_ACCOUNT_IN_USE' "the voice app $($app.Name) also answers for the resource account $other, which this hotline does not name; nothing was removed"
            }
        }
    }
    $apps = @{ 'AutoAttendant' = [System.Collections.Generic.List[string]]::new(); 'CallQueue' = [System.Collections.Generic.List[string]]::new() }
    foreach ($kind in 'AutoAttendant', 'CallQueue') {
        foreach ($id in $ids) {
            $assoc = Get-Association $id
            if ($assoc -and [string]$assoc.ConfigurationType -eq $kind) {
                $r = Remove-VoiceAppOf $kind $id ''
                if ($r.removed) { $apps[$kind].Add([string]$r.id) }
            }
        }
        foreach ($name in $names) {
            $r = Remove-VoiceAppOf $kind '' $name
            if ($r.removed) { $apps[$kind].Add([string]$r.id) }
        }
    }
    $accounts = [System.Collections.Generic.List[string]]::new()
    $numbers = [System.Collections.Generic.List[string]]::new()
    foreach ($id in $ids) {
        $r = Remove-Account $id
        if (-not $r.removed) { continue }
        $accounts.Add($id)
        foreach ($n in $r.phoneNumbers) { $numbers.Add([string]$n) }
    }
    $total = $apps['AutoAttendant'].Count + $apps['CallQueue'].Count + $accounts.Count + $numbers.Count
    return [ordered]@{
        removed = ($total -gt 0)
        attendants = @(Get-SortedIds $apps['AutoAttendant']); queues = @(Get-SortedIds $apps['CallQueue'])
        accounts = @(Get-SortedIds $accounts); phoneNumbers = @(Get-SortedIds $numbers)
    }
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
        'ensure-phone-number' { Set-PhoneNumber }
        'remove-phone-number' { Remove-PhoneNumber }
        'remove-hotline' { Remove-Hotline }
        'list-free-phone-numbers' { Get-FreePhoneNumbers }
        default { throw "unknown operation $($Request.operation)" }
    }
    $envelope = [ordered]@{ ok = $true; result = $result }
} catch {
    $envelope = [ordered]@{ ok = $false; error = $_.Exception.Message }
    $code = $_.Exception.Data['atlasCode']
    if ($code) { $envelope.code = [string]$code }
} finally {
    try { $null = Disconnect-MicrosoftTeams -ErrorAction SilentlyContinue } catch { }
}
# ASCII only, so the answer reads the same whatever code page the host's console has.
[Console]::Out.WriteLine(($envelope | ConvertTo-Json -Depth 8 -Compress -EscapeHandling EscapeNonAscii))
