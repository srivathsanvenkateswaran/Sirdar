//go:build windows

package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"unicode/utf16"
)

// PlatformSecretStore returns the credential store this operating system
// ships with: on Windows the Credential Manager, read through a short
// PowerShell snippet that calls CredRead.
func PlatformSecretStore() SecretStore { return CredentialManager{} }

// credNotFound is the exit status the PowerShell reader uses for "the
// Credential Manager has no such entry", to keep it apart from the status
// PowerShell itself exits with when the snippet fails.
const credNotFound = 3

// credTargetEnv carries the credential's name into PowerShell. Passing it
// in the environment rather than in the command text means a service name
// with a quote or a semicolon in it cannot become PowerShell code.
const credTargetEnv = "SIRDAR_CRED_TARGET"

// CredentialManager reads a generic credential from the Windows Credential
// Manager.
//
// There is no command-line tool that will print a stored secret: `cmdkey`
// lists entries and stores them but never reveals a password, by design.
// The supported read path is the CredRead API, so this runs a PowerShell
// snippet that declares the P/Invoke with Add-Type and calls it. That needs
// nothing installed beyond Windows PowerShell itself — in particular it
// does not need the PowerShell Gallery's CredentialManager module, whose
// Get-StoredCredential does the same job for operators who already have it
// (docs/credentials.md says how).
//
// The secret comes back on standard output and goes nowhere else: the
// snippet writes it with [Console]::Out.Write and nothing echoes it.
type CredentialManager struct{}

func (CredentialManager) Read(service string) (string, error) {
	ref := "keychain:" + service
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return "", fmt.Errorf("%s: powershell is not on PATH, so the Credential Manager cannot be read; use an env:, file: or cmd: ref instead", ref)
	}

	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", credReadEncoded)
	cmd.Env = append(os.Environ(), credTargetEnv+"="+service)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == credNotFound {
			return "", &NotFoundError{Ref: ref, Detail: "no generic credential named " + service + " in the Windows Credential Manager"}
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s: reading the Credential Manager failed: %s", ref, msg)
		}
		return "", fmt.Errorf("%s: reading the Credential Manager failed: %w", ref, err)
	}

	secret := trimOneNewline(stdout.String())
	if secret == "" {
		return "", &NotFoundError{Ref: ref, Detail: "the generic credential named " + service + " has an empty password"}
	}
	return secret, nil
}

// credReadScript is run by PowerShell to print one generic credential's
// password. It reads the target name from the environment, returns exit
// status 3 when CredRead reports no such entry, and writes the password
// with no trailing newline.
const credReadScript = `
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition '
using System;
using System.Runtime.InteropServices;
public static class SirdarCred {
  [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  private static extern bool CredReadW(string target, int type, int flags, out IntPtr credential);
  [DllImport("advapi32.dll")]
  private static extern void CredFree(IntPtr buffer);
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  private struct CREDENTIAL {
    public int Flags;
    public int Type;
    public IntPtr TargetName;
    public IntPtr Comment;
    public long LastWritten;
    public int CredentialBlobSize;
    public IntPtr CredentialBlob;
    public int Persist;
    public int AttributeCount;
    public IntPtr Attributes;
    public IntPtr TargetAlias;
    public IntPtr UserName;
  }
  public static string Read(string target) {
    IntPtr handle;
    if (!CredReadW(target, 1, 0, out handle)) { return null; }
    try {
      CREDENTIAL c = (CREDENTIAL)Marshal.PtrToStructure(handle, typeof(CREDENTIAL));
      if (c.CredentialBlobSize <= 0) { return String.Empty; }
      return Marshal.PtrToStringUni(c.CredentialBlob, c.CredentialBlobSize / 2);
    } finally { CredFree(handle); }
  }
}
'
$target = [Environment]::GetEnvironmentVariable('SIRDAR_CRED_TARGET')
$value = [SirdarCred]::Read($target)
if ($null -eq $value) { exit 3 }
[Console]::Out.Write($value)
`

// credReadEncoded is credReadScript in the form PowerShell's
// -EncodedCommand takes: UTF-16LE, base64. The script is passed that way
// rather than as -Command text because the C# it carries is full of double
// quotes, and every layer between Go's argv quoting and PowerShell's own
// parser would otherwise have to be got exactly right.
var credReadEncoded = encodePowerShell(credReadScript)

func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(units)*2)
	for _, u := range units {
		b = append(b, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
