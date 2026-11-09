package sharepointserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
)

// harness stands in for a farm inside a real PowerShell: functions of the cmdlets'
// names, which PowerShell resolves ahead of a cmdlet, over an in-memory site list. It
// exercises the bootstrap's own logic (dispatch, retries, the JSON answer); it cannot
// stand in for SharePoint's parameter binding, its object model, or the directory.
const harness = `
function Get-PSSnapin { param($Name, [switch]$Registered) return $true }
function Add-PSSnapin { param($Name) }
$global:Sites = @{}
$global:Deleted = @()
$global:AddedGroups = 0
function New-User($login) { [pscustomobject]@{ ID = $login.GetHashCode(); LoginName = $login; IsSiteAdmin = $false } | Add-Member -PassThru ScriptMethod Update { } }
function New-Group { $g = [pscustomobject]@{ Users = @() }
  $g | Add-Member ScriptMethod AddUser { param($u) if (-not ($this.Users | Where-Object ID -eq $u.ID)) { $this.Users = @($this.Users) + $u } }
  $g | Add-Member ScriptMethod RemoveUser { param($u) $this.Users = @($this.Users | Where-Object ID -ne $u.ID) }
  return $g }
function New-FakeSite($url, $name, $owner) {
  $web = [pscustomobject]@{ Title = $name; Description = ''; Language = 1031; WebTemplate = 'STS'; Configuration = 0
    AllProperties = @{}; AssociatedOwnerGroup = $null; AssociatedMemberGroup = $null; AssociatedVisitorGroup = $null
    SiteAdministrators = @(); UsersByLogin = @{} }
  $web | Add-Member ScriptMethod Dispose { }
  $web | Add-Member ScriptMethod Update { }
  $web | Add-Member ScriptMethod EnsureUser { param($x) if (-not $this.UsersByLogin.ContainsKey($x)) { $this.UsersByLogin[$x] = New-User $x }; return $this.UsersByLogin[$x] }
  $web | Add-Member ScriptMethod CreateDefaultAssociatedGroups { param($o, $s, $seed) $global:AddedGroups++; $this.AssociatedOwnerGroup = New-Group; $this.AssociatedMemberGroup = New-Group; $this.AssociatedVisitorGroup = New-Group }
  $site = [pscustomobject]@{ Url = $url; ServerRelativeUrl = ([uri]$url).AbsolutePath; RootWeb = $web
    Owner = (New-User $owner); SecondaryContact = $null; ContentDatabase = [pscustomobject]@{ Name = 'WSS_Content' }
    Quota = [pscustomobject]@{ StorageMaximumLevel = 0; StorageWarningLevel = 0; QuotaID = 0 }; Usage = [pscustomobject]@{ Storage = 5 } }
  $site | Add-Member ScriptMethod Dispose { }
  return $site
}
function Get-SPSite { [CmdletBinding()] param($Identity, $WebApplication, $Limit)
  if ($WebApplication) { return @($global:Sites.Values) }
  if ($global:Sites.ContainsKey($Identity)) { return $global:Sites[$Identity] }
  Write-Error 'not found' }
function New-SPSite { [CmdletBinding()] param($Url, $Name, $OwnerAlias, $Template, $SecondaryOwnerAlias, $OwnerEmail, $Description, $ContentDatabase, $QuotaTemplate, [uint32]$Language)
  $s = New-FakeSite $Url $Name $OwnerAlias; $global:Sites[$Url] = $s; return $s }
function Set-SPSite { [CmdletBinding()] param($Identity, $OwnerAlias, $SecondaryOwnerAlias, [long]$MaxSize, [long]$WarningSize)
  $s = $global:Sites[$Identity]
  if ($OwnerAlias) { $s.Owner = New-User $OwnerAlias }
  if ($MaxSize) { $s.Quota.StorageMaximumLevel = $MaxSize; $s.Quota.StorageWarningLevel = $WarningSize } }
function Remove-SPSite { [CmdletBinding(SupportsShouldProcess)] param($Identity, [switch]$GradualDelete)
  if (-not $GradualDelete) { throw 'must be gradual' }
  $s = $global:Sites[$Identity]; $global:Sites.Remove($Identity)
  $global:Deleted += [pscustomobject]@{ SiteId = 'guid-1'; Path = $s.ServerRelativeUrl; DeletionTime = (Get-Date); Site = $s } }
function Get-SPDeletedSite { [CmdletBinding()] param($Identity) @($global:Deleted | Where-Object Path -eq $Identity) }
function Restore-SPDeletedSite { [CmdletBinding(SupportsShouldProcess)] param($Identity, $ContentDatabase)
  $d = $global:Deleted | Where-Object SiteId -eq $Identity | Select-Object -First 1
  $global:Sites[$d.Site.Url] = $d.Site; $global:Deleted = @() }
`

// run runs the bootstrap in real PowerShell with the harness in front of it. The farm
// keeps nothing between calls, so a test states several operations in one script.
func runFarm(t *testing.T, ops []catalog.Invocation, prelude string) []map[string]any {
	t.Helper()
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	const web = "http://sp01"
	var script strings.Builder
	script.WriteString(harness)
	script.WriteString(prelude)
	// The Linux PowerShell has no NTAccount directory: an account named *missing*
	// fails the lookup, every other one is known.
	script.WriteString(`
function Test-AtlasFakeAccount { }
$bootstrap = @'
` + strings.ReplaceAll(strings.ReplaceAll(bootstrap, "'@", "' @"), "New-Object System.Security.Principal.NTAccount($plain)", "(New-AtlasFakeAccount $plain)") + `
'@
function New-AtlasFakeAccount($p) { if ($p -like '*missing*') { throw 'no such account' }; return [pscustomobject]@{} | Add-Member -PassThru ScriptMethod Translate { param($t) 1 } }
`)
	for _, inv := range ops {
		req := request{Op: inv.Op, WebApplication: web, Args: inv.Args}
		if inv.Site != "" {
			req.SitePath = "/sites/" + inv.Site
			req.SiteURL = web + req.SitePath
		}
		raw, _ := json.Marshal(req)
		script.WriteString("$env:ATLAS_SHAREPOINTSERVER_REQUEST = @'\n" + string(raw) + "\n'@\n")
		script.WriteString("& ([scriptblock]::Create($bootstrap))\n")
	}
	file := filepath.Join(t.TempDir(), "farm.ps1")
	if err := os.WriteFile(file, []byte(script.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", file).CombinedOutput()
	if err != nil {
		t.Fatalf("pwsh: %v\n%s", err, out)
	}
	var answers []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		answers = append(answers, m)
	}
	if len(answers) != len(ops) {
		tail := out
		if len(tail) > 1500 {
			tail = tail[len(tail)-1500:]
		}
		t.Fatalf("%d answers for %d operations:\n%s", len(answers), len(ops), tail)
	}
	return answers
}

func inv(op, site string, args map[string]any) catalog.Invocation {
	if args == nil {
		args = map[string]any{}
	}
	return catalog.Invocation{Op: op, Site: site, Args: args}
}

func site(t *testing.T, a map[string]any) map[string]any {
	t.Helper()
	if a["ok"] != true {
		t.Fatalf("answer = %v", a)
	}
	v := a["value"].([]any)
	if len(v) != 1 {
		t.Fatalf("value = %v", v)
	}
	return v[0].(map[string]any)
}

func wantCode(t *testing.T, a map[string]any, code string) {
	t.Helper()
	if a["ok"] != false {
		t.Fatalf("answer = %v, want %s", a, code)
	}
	if got := a["error"].(map[string]any)["code"]; got != code {
		t.Errorf("code = %v, want %s", got, code)
	}
}

func TestBootstrapLifecycleOnAFakeFarm(t *testing.T) {
	create := map[string]any{"Name": "Alpha", "OwnerAlias": `C\anna`, "Template": "STS#0", "Language": 1031, "Properties": map[string]any{"atlas.position": "p1"}}
	a := runFarm(t, []catalog.Invocation{
		inv("create-site", "alpha", create),
		inv("create-site", "alpha", create),
		inv("get-site", "nope", nil),
		inv("get-site", "alpha", nil),
		inv("update-group-members", "alpha", map[string]any{"Group": "members", "Add": []string{`C\team`, `C\team`}}),
		inv("update-group-members", "alpha", map[string]any{"Group": "members", "Remove": []string{`C\team`, `C\gone`}}),
		inv("update-site-admins", "alpha", map[string]any{"Add": []string{`C\bob`}}),
		inv("update-site-admins", "alpha", map[string]any{"Remove": []string{`C\bob`}}),
		inv("set-site-owners", "alpha", map[string]any{"OwnerAlias": `C\carl`}),
		inv("update-site-admins", "alpha", map[string]any{"Remove": []string{`C\carl`}}),
		inv("set-site-quota", "alpha", map[string]any{"MaxSize": 2048, "WarningSize": 1024}),
		inv("list-sites", "", map[string]any{"Limit": 200}),
		inv("delete-site", "alpha", nil),
		inv("delete-site", "alpha", nil),
		inv("restore-site", "alpha", nil),
		inv("restore-site", "alpha", nil),
		inv("delete-site", "nowhere", nil),
		inv("restore-site", "nowhere", nil),
		inv("set-site-owners", "alpha", map[string]any{"OwnerAlias": `C\missing`}),
		inv("create-site", "beta", map[string]any{"Name": "B", "OwnerAlias": `C\missing`, "Template": "STS#0"}),
		inv("update-group-members", "nowhere", map[string]any{"Group": "members", "Add": []string{`C\x`}}),
	}, "")
	first := site(t, a[0])
	if first["Url"] != "http://sp01/sites/alpha" || first["Language"] != float64(1031) || first["Properties"].(map[string]any)["atlas.position"] != "p1" {
		t.Errorf("created = %v", first)
	}
	wantCode(t, a[1], CodeSiteExists)
	if a[2]["ok"] != true || len(a[2]["value"].([]any)) != 0 {
		t.Errorf("an absent site is an empty answer, not a failure: %v", a[2])
	}
	if s := site(t, a[3]); s["Url"] != "http://sp01/sites/alpha" {
		t.Errorf("read = %v", s)
	}
	for _, i := range []int{4, 5, 6, 7, 8, 10, 12, 13} {
		if a[i]["ok"] != true {
			t.Errorf("step %d failed: %v", i, a[i])
		}
	}
	wantCode(t, a[9], CodeOwnerRequired)
	if v := a[11]["value"].([]any); len(v) != 1 {
		t.Errorf("listing = %v", v)
	}
	if s := site(t, a[14]); s["Url"] != "http://sp01/sites/alpha" {
		t.Errorf("restored = %v", s)
	}
	if s := site(t, a[15]); s["Url"] != "http://sp01/sites/alpha" {
		t.Errorf("restoring what is back succeeds: %v", s)
	}
	wantCode(t, a[16], CodeSiteNotFound)
	wantCode(t, a[17], CodeDeletedSiteNotFound)
	wantCode(t, a[18], CodeAccountNotFound)
	wantCode(t, a[19], CodeAccountNotFound)
	wantCode(t, a[20], CodeSiteNotFound)
}

func TestBootstrapResumesASiteWithoutItsGroups(t *testing.T) {
	a := runFarm(t, []catalog.Invocation{
		inv("create-site", "half", map[string]any{"Name": "H", "OwnerAlias": `C\a`, "Template": "STS#0"}),
	}, "\n$global:Sites['http://sp01/sites/half'] = New-FakeSite 'http://sp01/sites/half' 'H' 'C\\a'\n")
	if s := site(t, a[0]); s["Title"] != "H" {
		t.Errorf("resumed = %v", s)
	}
}

func TestBootstrapRefusesAQuotaOnATemplate(t *testing.T) {
	a := runFarm(t, []catalog.Invocation{
		inv("set-site-quota", "q", map[string]any{"MaxSize": 1, "WarningSize": 1}),
		inv("update-group-members", "q", map[string]any{"Group": "members", "Add": []string{`C\a`}}),
		inv("no-such-op", "", nil),
	}, "\n$s = New-FakeSite 'http://sp01/sites/q' 'Q' 'C\\a'; $s.Quota.QuotaID = 3; $global:Sites['http://sp01/sites/q'] = $s\n")
	wantCode(t, a[0], CodeQuotaRefused)
	wantCode(t, a[1], CodeGroupMissing)
	if a[2]["ok"] != false || !strings.Contains(a[2]["error"].(map[string]any)["message"].(string), "Unknown operation") {
		t.Errorf("unknown = %v", a[2])
	}
}
