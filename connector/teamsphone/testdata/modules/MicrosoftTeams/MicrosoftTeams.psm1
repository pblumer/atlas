# A stand-in for the MicrosoftTeams module, for the teamsphone tests only. It keeps a
# tenant in a JSON file under the temporary directory, because every job is its own
# pwsh process, and it models the one behaviour the shipped script exists to handle:
# a resource account Teams shows only after a synchronization. An account becomes
# visible to Teams three sign-ins after it was created; the test's Graph stand-in reads
# the same file and knows it at once.

$script:StatePath = Join-Path ([IO.Path]::GetTempPath()) 'teamsphone-stub.json'

function Get-State {
    if (Test-Path $script:StatePath) {
        return Get-Content -Raw -Encoding utf8 $script:StatePath | ConvertFrom-Json -AsHashtable
    }
    return @{ connects = 0; sets = 0; users = @{}; accounts = @{}; apps = @{}; assoc = @{}; schedules = @(); entra = @{}; numbers = @{} }
}

function Save-State($State) {
    $State | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 $script:StatePath
}

function Get-NewId { [guid]::NewGuid().ToString() }

function Find-Account($State, [string]$Identity) {
    foreach ($a in $State.accounts.Values) {
        if ($a.ObjectId -eq $Identity -or $a.UserPrincipalName -eq $Identity) { return $a }
    }
    return $null
}

function Connect-MicrosoftTeams {
    param([string]$ApplicationId, [string]$TenantId, $Certificate)
    if (-not ($Certificate -is [System.Security.Cryptography.X509Certificates.X509Certificate2]) -or -not $Certificate.HasPrivateKey) {
        throw 'stub: Connect-MicrosoftTeams needs a certificate with its private key'
    }
    if (-not $ApplicationId -or -not $TenantId) { throw 'stub: Connect-MicrosoftTeams needs an application and a tenant' }
    $s = Get-State
    $s.connects = [int]$s.connects + 1
    Save-State $s
    # The real cmdlet answers with an account object; the script must not let it out.
    [pscustomobject]@{ Account = $ApplicationId; TenantId = $TenantId }
}

function Disconnect-MicrosoftTeams { }

function Get-CsOnlineUser {
    param([string]$Identity)
    $s = Get-State
    $id = $s.users[$Identity.ToLowerInvariant()]
    if (-not $id) { throw "Management object not found for identity $Identity" }
    [pscustomobject]@{ Identity = $id; UserPrincipalName = $Identity }
}

function Get-CsOnlineApplicationInstance {
    param([string]$Identity)
    $s = Get-State
    $a = Find-Account $s $Identity
    if (-not $a -or [int]$s.connects -lt [int]$a.VisibleFrom) { throw "Couldn't find an application instance $Identity" }
    [pscustomobject]$a
}

function New-CsOnlineApplicationInstance {
    param([string]$UserPrincipalName, [string]$DisplayName, [string]$ApplicationId)
    $s = Get-State
    if ((Find-Account $s $UserPrincipalName) -or $s.entra.ContainsKey($UserPrincipalName.ToLowerInvariant())) {
        throw 'An object with the same userPrincipalName already exists.'
    }
    $a = @{ ObjectId = (Get-NewId); UserPrincipalName = $UserPrincipalName; DisplayName = $DisplayName
        ApplicationId = $ApplicationId; VisibleFrom = [int]$s.connects + 3 }
    $s.accounts[$a.ObjectId] = $a
    Save-State $s
    [pscustomobject]$a
}

function Get-CsOnlineApplicationInstanceAssociation {
    param([string]$Identity)
    $s = Get-State
    $x = $s.assoc[$Identity]
    if (-not $x) { throw "no association for $Identity" }
    [pscustomobject]@{ Id = $Identity; ConfigurationId = $x.ConfigurationId; ConfigurationType = $x.ConfigurationType }
}

function New-CsOnlineApplicationInstanceAssociation {
    param([string[]]$Identities, [string]$ConfigurationId, [string]$ConfigurationType)
    $s = Get-State
    foreach ($i in $Identities) {
        $a = Find-Account $s $i
        if (-not $a -or [int]$s.connects -lt [int]$a.VisibleFrom) { throw "stub: $i is no application instance Teams knows" }
        if (-not $s.apps[$ConfigurationId]) { throw "stub: no voice app $ConfigurationId" }
        $s.assoc[$i] = @{ ConfigurationId = $ConfigurationId; ConfigurationType = $ConfigurationType }
    }
    Save-State $s
    [pscustomobject]@{ Results = @($Identities) }
}

function Remove-CsOnlineApplicationInstanceAssociation {
    param([string[]]$Identities)
    $s = Get-State
    foreach ($i in $Identities) { $s.assoc.Remove($i) }
    Save-State $s
}

function ConvertTo-App($State, $App) {
    $instances = @($State.assoc.Keys | Where-Object { $State.assoc[$_].ConfigurationId -eq $App.Identity })
    $o = [pscustomobject]$App
    $o | Add-Member -NotePropertyName ApplicationInstances -NotePropertyValue $instances
    $o
}

function Get-App([string]$Kind, [string]$Identity, [string]$NameFilter) {
    $s = Get-State
    if ($Identity) {
        $app = $s.apps[$Identity]
        if (-not $app -or $app.Kind -ne $Kind) { throw "stub: no $Kind $Identity" }
        return ConvertTo-App $s $app
    }
    foreach ($app in @($s.apps.Values | Where-Object { $_.Kind -eq $Kind -and $_.Name -like "*$NameFilter*" })) {
        ConvertTo-App $s $app
    }
}

function New-App([string]$Kind, [hashtable]$Params) {
    $s = Get-State
    $app = @{ Identity = (Get-NewId); Kind = $Kind; Name = $Params.Name; Params = @{} }
    foreach ($k in $Params.Keys) {
        if ($k -in 'DefaultCallFlow', 'CallFlows', 'CallHandlingAssociations') { continue }
        $app.Params[$k] = $Params[$k]
    }
    # A call queue answers with its settings and with the agents named directly as Users,
    # as Teams' does.
    if ($Kind -eq 'CallQueue') {
        $app.Users = [string[]]@(foreach ($u in $Params.Users) { $u })
        $app.AuthorizedUsers = [string[]]@(foreach ($u in $Params.AuthorizedUsers) { $u })
        $app.RoutingMethod = [string]$Params.RoutingMethod
        $app.PresenceBasedRouting = [bool]$Params.PresenceBasedRouting
        $app.ConferenceMode = [bool]$Params.ConferenceMode
        $app.AllowOptOut = [bool]$Params.AllowOptOut
    }
    if ($Params.CallHandlingAssociations) { $app.Params.Holidays = @($Params.CallHandlingAssociations).Count }
    $s.apps[$app.Identity] = $app
    Save-State $s
    ConvertTo-App $s $app
}

function Remove-App([string]$Kind, [string]$Identity) {
    $s = Get-State
    if (-not $s.apps[$Identity]) { throw "stub: no $Kind $Identity" }
    $s.apps.Remove($Identity)
    Save-State $s
}

function Assert-Agents($Users) {
    # Teams takes agents as ids; a blank one is a list the script assembled wrongly.
    foreach ($u in $Users) { if (-not $u) { throw 'stub: an agent id is blank' } }
}

function Get-CsCallQueue { param([string]$Identity, [string]$NameFilter) Get-App 'CallQueue' $Identity $NameFilter }
function New-CsCallQueue {
    param([string]$Name, [switch]$UseDefaultMusicOnHold, [string]$RoutingMethod, [bool]$PresenceBasedRouting,
        [bool]$ConferenceMode, [bool]$AllowOptOut, [string]$LanguageId, [string[]]$Users, [string[]]$AuthorizedUsers)
    Assert-Agents $Users
    Assert-Agents $AuthorizedUsers
    New-App 'CallQueue' $PSBoundParameters
}
# Changes only what it is given, as Teams' does, and counts its calls so a test sees that
# a run with nothing to change makes none.
function Set-CsCallQueue {
    param([string]$Identity, [string[]]$Users, [string[]]$AuthorizedUsers, [string]$RoutingMethod,
        [bool]$PresenceBasedRouting, [bool]$ConferenceMode, [bool]$AllowOptOut)
    Assert-Agents $Users
    Assert-Agents $AuthorizedUsers
    $s = Get-State
    $app = $s.apps[$Identity]
    if (-not $app -or $app.Kind -ne 'CallQueue') { throw "stub: no CallQueue $Identity" }
    foreach ($k in $PSBoundParameters.Keys) {
        if ($k -eq 'Identity') { continue }
        $v = $PSBoundParameters[$k]
        # Assigned in each branch: an if used as a value unrolls a one-element list.
        if ($k -in 'Users', 'AuthorizedUsers') { $app[$k] = [string[]]@(foreach ($u in $v) { $u }) } else { $app[$k] = $v }
    }
    $s.sets = [int]$s.sets + 1
    Save-State $s
}
function Remove-CsCallQueue { param([string]$Identity) Remove-App 'CallQueue' $Identity }

function Get-CsAutoAttendant { param([string]$Identity, [string]$NameFilter) Get-App 'AutoAttendant' $Identity $NameFilter }
function New-CsAutoAttendant {
    param([string]$Name, [string]$LanguageId, [string]$TimeZoneId, $DefaultCallFlow, $CallFlows,
        $CallHandlingAssociations, [string[]]$AuthorizedUsers)
    if (-not $DefaultCallFlow.Menu.MenuOptions[0].CallTarget.Identity) { throw 'stub: the default call flow transfers nowhere' }
    $p = @{} + $PSBoundParameters
    $p.TransferTarget = $DefaultCallFlow.Menu.MenuOptions[0].CallTarget.Identity
    New-App 'AutoAttendant' $p
}
function Remove-CsAutoAttendant { param([string]$Identity) Remove-App 'AutoAttendant' $Identity }

function New-CsAutoAttendantCallableEntity { param([string]$Identity, [string]$Type) [pscustomobject]@{ Identity = $Identity; Type = $Type } }
function New-CsAutoAttendantMenuOption { param([string]$Action, [string]$DtmfResponse, $CallTarget) [pscustomobject]@{ Action = $Action; CallTarget = $CallTarget } }
function New-CsAutoAttendantMenu { param([string]$Name, $MenuOptions) [pscustomobject]@{ Name = $Name; MenuOptions = @($MenuOptions) } }
function New-CsAutoAttendantCallFlow { param([string]$Name, $Menu) [pscustomobject]@{ Id = (Get-NewId); Name = $Name; Menu = $Menu } }
function New-CsOnlineDateTimeRange {
    param([string]$Start, [string]$End)
    [pscustomobject]@{ Start = $Start; End = $End }
}
function New-CsOnlineSchedule {
    param([string]$Name, [switch]$FixedSchedule, $DateTimeRanges)
    $s = Get-State
    $s.schedules = @($s.schedules) + @(@{ Name = $Name; Start = $DateTimeRanges[0].Start; End = $DateTimeRanges[0].End })
    Save-State $s
    [pscustomobject]@{ Id = (Get-NewId); Name = $Name }
}
function New-CsAutoAttendantCallHandlingAssociation {
    param([string]$Type, [string]$ScheduleId, [string]$CallFlowId)
    [pscustomobject]@{ Type = $Type; ScheduleId = $ScheduleId; CallFlowId = $CallFlowId }
}

# Phone numbers. The inventory is the state's numbers, keyed by number; a Direct Routing
# number joins it when assigned and leaves it when removed, as the operator's carrier
# serves it rather than Microsoft. A target is held by its object id, as Teams holds it.
function Resolve-Target($State, [string]$Identity) {
    $a = Find-Account $State $Identity
    if ($a) { return [string]$a.ObjectId }
    $u = $State.users[$Identity.ToLowerInvariant()]
    if ($u) { return [string]$u }
    if ($State.users.Values -contains $Identity) { return $Identity }
    throw "stub: no user or resource account $Identity"
}

# Answers as Teams does: sorted by number, at most Top (500 unless told), with the
# filters the shipped script uses applied here rather than left to it. A number the
# state does not describe further is activated, assignable to anyone and not blocked.
function Get-CsPhoneNumberAssignment {
    param([string]$TelephoneNumber, [string]$AssignedPstnTargetId, [string]$PstnAssignmentStatus, [string]$ActivationState,
        [string]$NumberType, [string]$CapabilitiesContain, [string]$TelephoneNumberStartsWith, [int]$Top = 500)
    if ($PstnAssignmentStatus -and $PstnAssignmentStatus -ne 'Unassigned') { throw "stub: PstnAssignmentStatus $PstnAssignmentStatus is not modelled" }
    $s = Get-State
    $target = if ($AssignedPstnTargetId) { Resolve-Target $s $AssignedPstnTargetId } else { $null }
    $out = foreach ($n in @($s.numbers.Values)) {
        $row = @{ ActivationState = 'Activated'; Capability = @('ConferenceAssignment', 'VoiceApplicationAssignment', 'UserAssignment'); AssignmentBlockedState = ''; City = ''; IsoCountryCode = '' }
        foreach ($k in $n.Keys) { $row[$k] = $n[$k] }
        if ($TelephoneNumber -and $row.TelephoneNumber -ne $TelephoneNumber) { continue }
        if ($target -and $row.AssignedPstnTargetId -ne $target) { continue }
        if ($PstnAssignmentStatus -and $row.AssignedPstnTargetId) { continue }
        if ($ActivationState -and $row.ActivationState -ne $ActivationState) { continue }
        if ($NumberType -and $row.NumberType -ne $NumberType) { continue }
        if ($TelephoneNumberStartsWith -and -not ([string]$row.TelephoneNumber).StartsWith($TelephoneNumberStartsWith, [StringComparison]::Ordinal)) { continue }
        if ($CapabilitiesContain -and @($CapabilitiesContain.Split(',') | Where-Object { @($row.Capability) -notcontains $_.Trim() }).Count -gt 0) { continue }
        [pscustomobject]$row
    }
    [object[]]$sorted = @($out)
    [string[]]$keys = @(foreach ($r in $sorted) { [string]$r.TelephoneNumber })
    [Array]::Sort($keys, $sorted, [StringComparer]::Ordinal)
    $sorted | Select-Object -First $Top
}

function Set-CsPhoneNumberAssignment {
    param([string]$Identity, [string]$TelephoneNumber, [string]$NumberType, [string]$LocationId)
    $s = Get-State
    $target = Resolve-Target $s $Identity
    $n = $s.numbers[$TelephoneNumber]
    if ($n -and $n.AssignedPstnTargetId -and $n.AssignedPstnTargetId -ne $target) { throw "stub: $TelephoneNumber is assigned to $($n.AssignedPstnTargetId)" }
    if (-not $n -and $NumberType -ne 'DirectRouting') { throw "stub: $TelephoneNumber is not in the tenant's inventory" }
    foreach ($other in @($s.numbers.Values)) {
        if ($other.AssignedPstnTargetId -eq $target -and $other.TelephoneNumber -ne $TelephoneNumber) {
            throw "stub: $Identity already has the primary number $($other.TelephoneNumber)"
        }
    }
    $s.numbers[$TelephoneNumber] = @{ TelephoneNumber = $TelephoneNumber; NumberType = $NumberType; AssignedPstnTargetId = $target; AssignmentCategory = 'Primary' }
    Save-State $s
}

function Remove-CsPhoneNumberAssignment {
    param([string]$Identity, [string]$TelephoneNumber, [string]$NumberType)
    $s = Get-State
    $target = Resolve-Target $s $Identity
    $n = $s.numbers[$TelephoneNumber]
    if (-not $n -or $n.AssignedPstnTargetId -ne $target) { throw "stub: $TelephoneNumber is not assigned to $Identity" }
    if ($n.NumberType -eq 'DirectRouting') { $s.numbers.Remove($TelephoneNumber) } else { $n.AssignedPstnTargetId = '' }
    Save-State $s
}

Export-ModuleMember -Function *-Cs*, Connect-MicrosoftTeams, Disconnect-MicrosoftTeams
