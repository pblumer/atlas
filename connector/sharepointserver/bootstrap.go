package sharepointserver

// bootstrap is the program PowerShell runs for every call. It is fixed text: nothing
// authored is ever spliced into it. Values arrive as JSON in the environment and are
// passed as arguments, so a value is data and never code.
//
// It targets Windows PowerShell 5.1, the host SharePoint's Management Shell is.
//
// Steps a reader would otherwise have to rediscover:
//   - New-SPSite makes no default groups; a site without them has no Members group to
//     add anyone to, so create-site asks for them itself. A site that exists but has
//     none is one this Worker created and could not finish, and is resumed.
//   - Accounts are checked through the directory (NTAccount.Translate) before a change,
//     because the cmdlets' own message is in the farm's language and a process needs a
//     code it can catch.
//   - Get-SPSite is compared to the URL it was asked for, so a site is never mistaken
//     for a neighbour that merely shares a prefix.
//   - Removing an account from a group it is not in, deleting a site already in the
//     recycle bin and restoring one already back all succeed: a job is delivered at
//     least once.
const bootstrap = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$script:AtlasCode = ''

function Stop-Atlas($code, $message) { $script:AtlasCode = $code; throw $message }

function Get-AtlasSite($url) {
    $s = Get-SPSite -Identity $url -ErrorAction SilentlyContinue
    if ($null -eq $s) { return $null }
    if ($s.Url -ne $url) { $s.Dispose(); return $null }
    return $s
}

function Get-AtlasSiteOrStop($url) {
    $s = Get-AtlasSite $url
    if ($null -eq $s) { Stop-Atlas 'SHAREPOINT_SERVER_SITE_NOT_FOUND' "No site at $url." }
    return $s
}

function Test-AtlasAccount($name) {
    $plain = $name
    if ($plain.Contains('|')) { $plain = $plain.Substring($plain.LastIndexOf('|') + 1) }
    try {
        [void](New-Object System.Security.Principal.NTAccount($plain)).Translate([System.Security.Principal.SecurityIdentifier])
    } catch {
        Stop-Atlas 'SHAREPOINT_SERVER_ACCOUNT_NOT_FOUND' "The directory does not know the account $name."
    }
}

function Get-AtlasLogins($users) {
    $out = @()
    foreach ($u in $users) { $out += [string]$u.LoginName }
    return ,$out
}

function ConvertTo-AtlasSite($s) {
    $web = $s.RootWeb
    try {
        $properties = @{}
        foreach ($k in $web.AllProperties.Keys) {
            if ([string]$k -like 'atlas.*') { $properties[[string]$k] = [string]$web.AllProperties[$k] }
        }
        $owners = @(); $members = @(); $visitors = @()
        if ($null -ne $web.AssociatedOwnerGroup) { $owners = Get-AtlasLogins $web.AssociatedOwnerGroup.Users }
        if ($null -ne $web.AssociatedMemberGroup) { $members = Get-AtlasLogins $web.AssociatedMemberGroup.Users }
        if ($null -ne $web.AssociatedVisitorGroup) { $visitors = Get-AtlasLogins $web.AssociatedVisitorGroup.Users }
        $secondary = $null
        if ($null -ne $s.SecondaryContact) { $secondary = [string]$s.SecondaryContact.LoginName }
        return [ordered]@{
            Url = [string]$s.Url
            ServerRelativeUrl = [string]$s.ServerRelativeUrl
            Title = [string]$web.Title
            Description = [string]$web.Description
            Language = [long]$web.Language
            Template = [string]$web.WebTemplate + '#' + [string]$web.Configuration
            Owner = [string]$s.Owner.LoginName
            SecondaryOwner = $secondary
            ContentDatabase = [string]$s.ContentDatabase.Name
            MaxSize = [long]$s.Quota.StorageMaximumLevel
            WarningSize = [long]$s.Quota.StorageWarningLevel
            QuotaTemplateId = [long]$s.Quota.QuotaID
            StorageUsed = [long]$s.Usage.Storage
            SiteCollectionAdmins = Get-AtlasLogins $web.SiteAdministrators
            OwnerGroup = $owners
            MemberGroup = $members
            VisitorGroup = $visitors
            Properties = $properties
        }
    } finally { $web.Dispose() }
}

function Read-AtlasSite($url) {
    $s = Get-AtlasSiteOrStop $url
    try { return ,@(ConvertTo-AtlasSite $s) } finally { $s.Dispose() }
}

function Invoke-AtlasCreate($req, $a) {
    Test-AtlasAccount $a.OwnerAlias
    if ($a.SecondaryOwnerAlias) { Test-AtlasAccount $a.SecondaryOwnerAlias }
    $s = Get-AtlasSite $req.siteUrl
    if ($null -ne $s) {
        $web = $s.RootWeb
        $unfinished = ($null -eq $web.AssociatedOwnerGroup)
        $web.Dispose()
        if (-not $unfinished) { $s.Dispose(); Stop-Atlas 'SHAREPOINT_SERVER_SITE_EXISTS' "A site already lives at $($req.siteUrl)." }
    } else {
        $p = @{ Url = $req.siteUrl; Name = [string]$a.Name; OwnerAlias = [string]$a.OwnerAlias; Template = [string]$a.Template; ErrorAction = 'Stop' }
        foreach ($k in 'SecondaryOwnerAlias', 'OwnerEmail', 'Description', 'ContentDatabase', 'QuotaTemplate') {
            if ($a.$k) { $p[$k] = [string]$a.$k }
        }
        if ($a.Language) { $p['Language'] = [uint32]$a.Language }
        $s = New-SPSite @p
    }
    try {
        $web = $s.RootWeb
        try {
            if ($null -eq $web.AssociatedOwnerGroup) {
                $second = $null
                if ($a.SecondaryOwnerAlias) { $second = [string]$a.SecondaryOwnerAlias }
                $web.CreateDefaultAssociatedGroups([string]$a.OwnerAlias, $second, '')
            }
            if ($null -ne $a.Properties) {
                foreach ($p in $a.Properties.PSObject.Properties) { $web.AllProperties[$p.Name] = [string]$p.Value }
                $web.Update()
            }
        } finally { $web.Dispose() }
    } finally { $s.Dispose() }
    return Read-AtlasSite $req.siteUrl
}

function Get-AtlasUser($web, $account) {
    Test-AtlasAccount $account
    return $web.EnsureUser($account)
}

function Invoke-AtlasAdmins($req, $a) {
    $s = Get-AtlasSiteOrStop $req.siteUrl
    try {
        $web = $s.RootWeb
        try {
            foreach ($x in @($a.Add)) {
                if ($x) { $u = Get-AtlasUser $web $x; if (-not $u.IsSiteAdmin) { $u.IsSiteAdmin = $true; $u.Update() } }
            }
            foreach ($x in @($a.Remove)) {
                if (-not $x) { continue }
                $u = Get-AtlasUser $web $x
                if ($u.ID -eq $s.Owner.ID -or ($null -ne $s.SecondaryContact -and $u.ID -eq $s.SecondaryContact.ID)) {
                    Stop-Atlas 'SHAREPOINT_SERVER_OWNER_REQUIRED' "$x is an owner of the site; set another owner first."
                }
                if ($u.IsSiteAdmin) { $u.IsSiteAdmin = $false; $u.Update() }
            }
        } finally { $web.Dispose() }
    } finally { $s.Dispose() }
}

function Invoke-AtlasMembers($req, $a) {
    $s = Get-AtlasSiteOrStop $req.siteUrl
    try {
        $web = $s.RootWeb
        try {
            $group = switch ([string]$a.Group) {
                'owners'   { $web.AssociatedOwnerGroup }
                'members'  { $web.AssociatedMemberGroup }
                'visitors' { $web.AssociatedVisitorGroup }
            }
            if ($null -eq $group) { Stop-Atlas 'SHAREPOINT_SERVER_GROUP_MISSING' "The site has no default $($a.Group) group." }
            foreach ($x in @($a.Add)) {
                if ($x) { $group.AddUser((Get-AtlasUser $web $x)) }
            }
            foreach ($x in @($a.Remove)) {
                if (-not $x) { continue }
                $u = Get-AtlasUser $web $x
                $present = $false
                foreach ($m in $group.Users) { if ($m.ID -eq $u.ID) { $present = $true } }
                if ($present) { $group.RemoveUser($u) }
            }
        } finally { $web.Dispose() }
    } finally { $s.Dispose() }
}

function Get-AtlasDeleted($path) {
    return @(Get-SPDeletedSite -Identity $path -ErrorAction SilentlyContinue)
}

function Invoke-AtlasDelete($req) {
    $s = Get-AtlasSite $req.siteUrl
    if ($null -eq $s) {
        if ((Get-AtlasDeleted $req.sitePath).Count -gt 0) { return }
        Stop-Atlas 'SHAREPOINT_SERVER_SITE_NOT_FOUND' "No site at $($req.siteUrl)."
    }
    $s.Dispose()
    Remove-SPSite -Identity $req.siteUrl -GradualDelete -Confirm:$false -ErrorAction Stop
}

function Invoke-AtlasRestore($req, $a) {
    $deleted = Get-AtlasDeleted $req.sitePath
    $s = Get-AtlasSite $req.siteUrl
    $exists = ($null -ne $s)
    if ($exists) { $s.Dispose() }
    if ($deleted.Count -eq 0) {
        if ($exists) { return Read-AtlasSite $req.siteUrl }
        Stop-Atlas 'SHAREPOINT_SERVER_DELETED_SITE_NOT_FOUND' "The recycle bin holds no deleted site for $($req.sitePath)."
    }
    if ($exists) { Stop-Atlas 'SHAREPOINT_SERVER_SITE_EXISTS' "A site already lives at $($req.siteUrl); it must go before a deleted one can come back." }
    $newest = $deleted | Sort-Object -Property DeletionTime -Descending | Select-Object -First 1
    $p = @{ Identity = $newest.SiteId; Confirm = $false; ErrorAction = 'Stop' }
    if ($a.ContentDatabase) { $p['ContentDatabase'] = [string]$a.ContentDatabase }
    Restore-SPDeletedSite @p
    return Read-AtlasSite $req.siteUrl
}

function Invoke-AtlasOwners($req, $a) {
    Test-AtlasAccount $a.OwnerAlias
    if ($a.SecondaryOwnerAlias) { Test-AtlasAccount $a.SecondaryOwnerAlias }
    $s = Get-AtlasSiteOrStop $req.siteUrl
    $s.Dispose()
    $p = @{ Identity = $req.siteUrl; OwnerAlias = [string]$a.OwnerAlias; ErrorAction = 'Stop' }
    if ($a.SecondaryOwnerAlias) { $p['SecondaryOwnerAlias'] = [string]$a.SecondaryOwnerAlias }
    Set-SPSite @p
}

function Invoke-AtlasQuota($req, $a) {
    $s = Get-AtlasSiteOrStop $req.siteUrl
    $template = [long]$s.Quota.QuotaID
    $s.Dispose()
    if ($template -ne 0) { Stop-Atlas 'SHAREPOINT_SERVER_QUOTA_REFUSED' 'The site uses a quota template, which excludes an individual quota.' }
    Set-SPSite -Identity $req.siteUrl -MaxSize ([long]$a.MaxSize) -WarningSize ([long]$a.WarningSize) -ErrorAction Stop
}

function Invoke-AtlasList($req, $a) {
    $limit = [int]$a.Limit + 1
    $out = @()
    $sites = @(Get-SPSite -WebApplication $req.webApplication -Limit $limit -ErrorAction Stop)
    foreach ($s in $sites) {
        try { $out += ,(ConvertTo-AtlasSite $s) } finally { $s.Dispose() }
    }
    return ,$out
}

$result = @{ ok = $false }
try {
    $req = $env:ATLAS_SHAREPOINTSERVER_REQUEST | ConvertFrom-Json
    if (Get-PSSnapin -Name Microsoft.SharePoint.PowerShell -Registered -ErrorAction SilentlyContinue) {
        Add-PSSnapin Microsoft.SharePoint.PowerShell -ErrorAction Stop
    } elseif (Get-Module -ListAvailable -Name SharePointServer) {
        Import-Module SharePointServer -DisableNameChecking -ErrorAction Stop
    } else {
        throw 'The SharePoint commands are not available here. This Worker runs on a server of the farm, under Windows PowerShell.'
    }
    $a = $req.args
    $value = @()
    switch ([string]$req.op) {
        'create-site'          { $value = Invoke-AtlasCreate $req $a }
        'get-site'             {
            $s = Get-AtlasSite $req.siteUrl
            if ($null -ne $s) { try { $value = @(ConvertTo-AtlasSite $s) } finally { $s.Dispose() } }
        }
        'list-sites'           { $value = Invoke-AtlasList $req $a }
        'delete-site'          { Invoke-AtlasDelete $req }
        'restore-site'         { $value = Invoke-AtlasRestore $req $a }
        'set-site-owners'      { Invoke-AtlasOwners $req $a }
        'update-site-admins'   { Invoke-AtlasAdmins $req $a }
        'update-group-members' { Invoke-AtlasMembers $req $a }
        'set-site-quota'       { Invoke-AtlasQuota $req $a }
        default                { throw "Unknown operation $($req.op)." }
    }
    $result = @{ ok = $true; value = @($value) }
} catch {
    $result = @{ ok = $false; error = @{ code = [string]$script:AtlasCode; message = [string]$_.Exception.Message } }
}
$json = ConvertTo-Json -InputObject $result -Depth 8 -Compress
$bytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($json + [Environment]::NewLine)
$out = [Console]::OpenStandardOutput()
$out.Write($bytes, 0, $bytes.Length)
$out.Flush()
`
