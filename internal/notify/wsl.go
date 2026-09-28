// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package notify

import (
	"encoding/base64"
	"unicode/utf16"
)

// powershellFallback is where Windows keeps PowerShell when WSL interop
// has not put it on PATH.
const powershellFallback = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

// toastAppID is PowerShell's own application id, the one Windows already
// trusts to show toasts, so no Start Menu registration is needed.
const toastAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toastScript reads the banner text from the environment, so the content
// never touches PowerShell quoting, and escapes it on the way into XML.
const toastScript = `
$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$title = [System.Security.SecurityElement]::Escape($env:GATE_INBOX_TOAST_TITLE)
$subtitle = [System.Security.SecurityElement]::Escape($env:GATE_INBOX_TOAST_SUBTITLE)
$body = [System.Security.SecurityElement]::Escape($env:GATE_INBOX_TOAST_BODY)
$sound = [System.Security.SecurityElement]::Escape($env:GATE_INBOX_TOAST_SOUND)
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml("<toast><visual><binding template='ToastGeneric'><text>$title</text><text>$subtitle</text><text>$body</text></binding></visual><audio src='$sound'/></toast>")
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:GATE_INBOX_TOAST_APPID).Show($toast)
`

func windowsToast(subtitle, body, sound string) error {
	shell, err := lookPath("powershell.exe")
	if err != nil {
		shell = powershellFallback
	}
	return runEnv(map[string]string{
		"GATE_INBOX_TOAST_TITLE":    appName,
		"GATE_INBOX_TOAST_SUBTITLE": subtitle,
		"GATE_INBOX_TOAST_BODY":     body,
		"GATE_INBOX_TOAST_SOUND":    sound,
		"GATE_INBOX_TOAST_APPID":    toastAppID,
	}, shell, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedCommand(toastScript))
}

// encodedCommand renders a script as -EncodedCommand takes it: UTF-16LE,
// then base64.
func encodedCommand(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		raw = append(raw, byte(unit), byte(unit>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}
