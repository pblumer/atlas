package exchange

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// pwshHarness stands in for an Exchange server inside a real PowerShell: it defines
// New-PSSession, Import-PSSession and Remove-PSSession as functions, which PowerShell
// resolves ahead of the cmdlets of the same name, and Import-PSSession hands back a
// module exporting only the fake Exchange commands the bootstrap asked for — which is
// what the real one does with a session's proxies.
//
// What it cannot stand in for is the session itself: WinRM, Kerberos, and Exchange's
// own parameter binding. Those are the parts of this backend no test here reaches.
const pwshHarness = `
function New-PSSession {
    [CmdletBinding()] param([string]$ConfigurationName, [string]$ConnectionUri, [string]$Authentication, $Credential)
    if ($ConnectionUri -like '*unreachable*') { throw 'Connecting to remote server exch01 failed with the following error message : WinRM cannot complete the operation.' }
    $user = ''
    if ($null -ne $Credential) { $user = $Credential.UserName + ':' + $Credential.GetNetworkCredential().Password }
    $global:AtlasOpened = [pscustomobject]@{ Configuration = $ConfigurationName; Uri = $ConnectionUri; Auth = $Authentication; User = $user }
    return 'session'
}
function Remove-PSSession { [CmdletBinding()] param($Session) $global:AtlasClosed = $true }
function Import-PSSession {
    [CmdletBinding()] param($Session, [string[]]$CommandName, [switch]$DisableNameChecking, [switch]$AllowClobber)
    New-Module -Name AtlasFakeExchange -ArgumentList (,$CommandName) -ScriptBlock {
        param($names)
        function Get-Mailbox {
            [CmdletBinding()] param($Identity, $ResultSize)
            if ($Identity -eq 'whoami') { return $global:AtlasOpened }
            foreach ($i in 1..3) {
                [pscustomobject]@{
                    DisplayName = 'Müller ' + $i
                    Count       = $i
                    Enabled     = $true
                    WhenCreated = [datetime]::new(2026, 10, 17, 9, 0, 0, [System.DateTimeKind]::Utc)
                    Addresses   = @(('SMTP:m' + $i + '@contoso.com'), 'smtp:alias@contoso.com')
                    Quota       = [System.Version]::new(1, 2)
                    Nothing     = $null
                    Size        = $ResultSize
                }
            }
        }
        function Set-Mailbox {
            [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
            param($Identity, $EmailAddresses, $ForwardingSmtpAddress)
            if (-not $PSCmdlet.ShouldProcess($Identity)) { throw 'declined' }
            [pscustomobject]@{
                IsHashtable = ($EmailAddresses -is [hashtable])
                Add         = $EmailAddresses['Add']
                Typed       = $EmailAddresses.ContainsKey('@odata.type')
                ForwardNull = ($null -eq $ForwardingSmtpAddress)
            }
        }
        function New-Mailbox {
            [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
            param([string]$Name, [switch]$Shared)
            if (-not $PSCmdlet.ShouldProcess($Name)) { throw 'declined' }
            [pscustomobject]@{ Name = $Name; Shared = [bool]$Shared }
        }
        function Add-MailboxPermission {
            [CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
            param($Identity, $User, $AccessRights, $InheritanceType)
            if (-not $PSCmdlet.ShouldProcess($Identity)) { throw 'declined' }
            [pscustomobject]@{ Rights = @($AccessRights).Count; First = @($AccessRights)[0]; Inheritance = $InheritanceType }
        }
        function Get-Thrower {
            [CmdletBinding()] param()
            Write-Error -Message "The operation couldn't be performed because object 'x' couldn't be found." -Category ObjectNotFound
            [pscustomobject]@{ Reached = $true }
        }
        Export-ModuleMember -Function $names
    }
}
`

// harnessRunner runs the bootstrap in a real pwsh with the harness in front of it.
type harnessRunner struct{ pwsh string }

func (h harnessRunner) Run(ctx context.Context, _ string, args, env []string) ([]byte, []byte, error) {
	script := decodeArg(args[len(args)-1])
	args = append(append([]string{}, args[:len(args)-1]...), encodeCommand(pwshHarness+script))
	return execRunner{}.Run(ctx, h.pwsh, args, env)
}

// decodeArg reverses encodeCommand; the argument is the one this package encoded, so
// it always decodes.
func decodeArg(b64 string) string {
	raw, _ := base64.StdEncoding.DecodeString(b64)
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return string(utf16.Decode(u))
}

func pwshClient(t *testing.T, cfg OnPremConfig) *OnPremClient {
	t.Helper()
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed; the bootstrap is only read, not run")
	}
	if cfg.ConnectionURI == "" {
		cfg.ConnectionURI = "http://exch01.contoso.local/PowerShell/"
	}
	cfg.Timeout = 2 * time.Minute
	c, err := NewOnPremClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.run = harnessRunner{pwsh: pwsh}
	return c
}

// TestBootstrapInRealPowerShell runs the bootstrap end to end in pwsh: the request read
// from the environment, the parameters bound by splatting, the answer flattened and
// written back as UTF-8.
func TestBootstrapInRealPowerShell(t *testing.T) {
	c := pwshClient(t, OnPremConfig{Username: `CONTOSO\svc`, Password: "pä$$ wörd"})
	ctx := context.Background()

	t.Run("listing is flattened and limited", func(t *testing.T) {
		out, err := c.Invoke(ctx, catalog.Invocation{Cmdlet: "Get-Mailbox", Parameters: map[string]any{"ResultSize": 3}}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Fatalf("limit 2 returned %d objects", len(out))
		}
		m := out[0].(map[string]any)
		if m["DisplayName"] != "Müller 1" {
			t.Errorf("DisplayName = %q (the answer is not UTF-8?)", m["DisplayName"])
		}
		if m["Enabled"] != true || m["WhenCreated"] != "2026-10-17T09:00:00.0000000Z" || m["Quota"] != "1.2" || m["Nothing"] != nil {
			t.Errorf("flattened object = %v", m)
		}
		if n, _ := m["Count"].(interface{ String() string }); n == nil || n.String() != "1" {
			t.Errorf("Count = %#v", m["Count"])
		}
		addrs, _ := m["Addresses"].([]any)
		if len(addrs) != 2 || addrs[0] != "SMTP:m1@contoso.com" {
			t.Errorf("Addresses = %#v", m["Addresses"])
		}
		if m["Size"] == nil {
			t.Error("the authored ResultSize did not reach the command")
		}
	})

	t.Run("session is opened with the configured credential", func(t *testing.T) {
		out, err := c.Invoke(ctx, catalog.Invocation{Cmdlet: "Get-Mailbox", Parameters: map[string]any{"Identity": "whoami"}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		m := out[0].(map[string]any)
		if m["Configuration"] != "Microsoft.Exchange" || m["Uri"] != "http://exch01.contoso.local/PowerShell/" || m["Auth"] != "Kerberos" || m["User"] != `CONTOSO\svc:pä$$ wörd` {
			t.Errorf("session = %v", m)
		}
	})

	t.Run("hashtables, nulls and confirmation", func(t *testing.T) {
		spec, _ := catalog.Lookup("add-email-address")
		inv, err := spec.Build("add-email-address", catalog.OnPrem, "", map[string]any{"Identity": "anna", "Address": "smtp:a@contoso.com"})
		if err != nil {
			t.Fatal(err)
		}
		inv.Parameters["ForwardingSmtpAddress"] = nil
		inv.Parameters["EmailAddresses"].(map[string]any)["@odata.type"] = "#Exchange.GenericHashTable"
		out, err := c.Invoke(ctx, inv, 0)
		if err != nil {
			t.Fatal(err)
		}
		m := out[0].(map[string]any)
		if m["IsHashtable"] != true || m["Add"] != "smtp:a@contoso.com" || m["Typed"] != false || m["ForwardNull"] != true {
			t.Errorf("Set-Mailbox saw %v", m)
		}
	})

	t.Run("switches and single-element arrays", func(t *testing.T) {
		spec, _ := catalog.Lookup("create-shared-mailbox")
		inv, _ := spec.Build("create-shared-mailbox", catalog.OnPrem, "", map[string]any{"Name": "Info"})
		out, err := c.Invoke(ctx, inv, 0)
		if err != nil {
			t.Fatal(err)
		}
		if m := out[0].(map[string]any); m["Shared"] != true || m["Name"] != "Info" {
			t.Errorf("New-Mailbox saw %v", m)
		}
		spec, _ = catalog.Lookup("add-full-access")
		inv, _ = spec.Build("add-full-access", catalog.OnPrem, "", map[string]any{"Identity": "info", "User": "anna"})
		out, err = c.Invoke(ctx, inv, 0)
		if err != nil {
			t.Fatal(err)
		}
		m := out[0].(map[string]any)
		if n, _ := m["Rights"].(interface{ String() string }); n == nil || n.String() != "1" || m["First"] != "FullAccess" || m["Inheritance"] != "All" {
			t.Errorf("Add-MailboxPermission saw %v", m)
		}
	})

	t.Run("an error is an error", func(t *testing.T) {
		_, err := c.Invoke(ctx, catalog.Invocation{Cmdlet: "Get-Thrower"}, 0)
		if err == nil || !strings.Contains(err.Error(), "couldn't be found") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a local command is not reachable", func(t *testing.T) {
		_, err := c.Invoke(ctx, catalog.Invocation{Cmdlet: "Get-Content", Parameters: map[string]any{"Path": "/etc/hostname"}}, 0)
		if err == nil || !strings.Contains(err.Error(), "offers no command Get-Content") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestBootstrapReportsASessionThatDoesNotOpen(t *testing.T) {
	c := pwshClient(t, OnPremConfig{ConnectionURI: "http://unreachable.contoso.local/PowerShell/"})
	_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "WinRM cannot complete") {
		t.Errorf("err = %v", err)
	}
}
