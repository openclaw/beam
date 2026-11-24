package journal

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPrivateFile(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkWindowsPrivacy(path, user.User.Sid, false); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRejectsWorldReadableJournalDirectory(t *testing.T) {
	dir := t.TempDir()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dir, "journal.db")); !errors.Is(err, errPublicJournal) {
		t.Fatalf("expected private-directory refusal, got %v", err)
	}
}
