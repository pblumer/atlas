//go:build windows

package ownerfile

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS — every right on a file — which x/sys/windows
// does not name. It is spelled out rather than GENERIC_ALL so the entry holds the
// file rights themselves and not a generic bit left for someone else to map.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

// Restrict replaces path's DACL with one entry granting this process's account
// every right, and protects it so the directory's inherited entries no longer apply.
func Restrict(path string) error {
	me, err := currentUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: fileAllAccess,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(me),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("ownerfile: build DACL for %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("ownerfile: set DACL on %s: %w", path, err)
	}
	return nil
}

// Check reports an error unless path's DACL is protected from inheritance and holds
// exactly one entry, an allow for this process's account.
func Check(path string) error {
	me, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("ownerfile: read DACL of %s: %w", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("ownerfile: read DACL of %s: %w", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("ownerfile: %s inherits its directory's access rights", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("ownerfile: read DACL of %s: %w", path, err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("ownerfile: %s grants access to more than this account", path)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return fmt.Errorf("ownerfile: read DACL of %s: %w", path, err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(me) {
		return fmt.Errorf("ownerfile: %s grants access to an account other than this one", path)
	}
	return nil
}

// currentUser is the SID of the account this process runs as.
func currentUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("ownerfile: read this process's account: %w", err)
	}
	return user.User.Sid, nil
}
