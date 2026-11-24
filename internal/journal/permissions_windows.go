package journal

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errPublicJournal = errors.New("Beam needs a private journal directory owned by your Windows account. Choose a new directory with --state")

// SQLite creates rollback, WAL and shared-memory files itself. A private
// inheritable directory ACL protects those files from the moment of creation.
func prepareJournal(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	if err := checkWindowsPrivacy(dir, user.User.Sid, true); err != nil {
		return err
	}
	// Upgrades may open an older database. Include existing SQLite companions
	// before allowing recovery or writing any private data.
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		err := checkWindowsPrivacy(path+suffix, user.User.Sid, false)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func trustedPrincipal(sid, user *windows.SID) bool {
	return sid != nil && (sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func checkWindowsPrivacy(path string, user *windows.SID, dir bool) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != dir {
		return errPublicJournal
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trustedPrincipal(owner, user) {
		return errPublicJournal
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errPublicJournal
	}
	var inheritable bool
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errPublicJournal
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trustedPrincipal(sid, user) && !sid.IsWellKnown(windows.WinCreatorOwnerSid) {
			return errPublicJournal
		}
		if ace.Header.AceFlags&windows.OBJECT_INHERIT_ACE != 0 {
			inheritable = true
		}
	}
	if dir && !inheritable {
		return errPublicJournal
	}
	return nil
}
