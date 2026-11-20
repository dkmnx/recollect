//go:build windows

package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createPrivateDir(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Create the directory plainly. Handing CreateDirectory a caller-built
	// security descriptor fails with ERROR_PRIVILEGE_NOT_HELD for some
	// tokens; .NET's CreateTempDirectoryWithAce fails the same way.
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	// Restrict afterwards. The process owns the directory it just created,
	// so replacing the DACL needs no privilege.
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

func validatePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("search cache path is not a plain directory")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("search cache DACL is not protected")
	}
	dacl, defaulted, err := sd.DACL()
	if err != nil || defaulted || dacl == nil || dacl.AceCount == 0 {
		return errors.New("search cache has no explicit DACL")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			!windows.EqualSid(sid, user.User.Sid) ||
			ace.Mask == 0 {
			return fmt.Errorf("search cache DACL entry %d rejected: type=%d sid=%s want=%s mask=%#x",
				i, ace.Header.AceType, sid.String(), user.User.Sid.String(), ace.Mask)
		}
	}
	return nil
}
