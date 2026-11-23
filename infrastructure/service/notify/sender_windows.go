//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// windowsSender shows a toast through PowerShell. Unpackaged apps identify
// themselves with a per-user AppUserModelId, which also carries the icon the
// toast header shows; the toast's own logo is the same image.
type windowsSender struct{ bin, app, icon string }

func platformSender(app, icon string) dservice.Notifier {
	if bin, err := exec.LookPath("powershell.exe"); err == nil {
		return windowsSender{bin: bin, app: app, icon: icon}
	}
	// Service environments can carry a thin PATH; try the standard location.
	if root := os.Getenv("SystemRoot"); root != "" {
		bin := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(bin); err == nil {
			return windowsSender{bin: bin, app: app, icon: icon}
		}
	}
	return none{reason: "powershell.exe not found"}
}

func (s windowsSender) Name() string    { return "powershell" }
func (s windowsSender) Available() bool { return true }
func (s windowsSender) Reason() string  { return "" }

func (s windowsSender) Notify(ctx context.Context, n dservice.Notification) error {
	title := n.Title
	if strings.TrimSpace(title) == "" {
		title = s.app
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$appId = %s
$key = 'HKCU:\Software\Classes\AppUserModelId\' + $appId
New-Item -Path $key -Force | Out-Null
New-ItemProperty -Path $key -Name DisplayName -Value %s -PropertyType String -Force | Out-Null
$icon = %s
if ($icon) { New-ItemProperty -Path $key -Name IconUri -Value $icon -PropertyType String -Force | Out-Null }
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] | Out-Null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml('<toast><visual><binding template="ToastGeneric"><image placement="appLogoOverride" hint-crop="circle"/><text></text><text></text></binding></visual></toast>')
$logo = $xml.GetElementsByTagName('image').Item(0)
if ($icon) { $logo.SetAttribute('src', 'file:///' + $icon.Replace('\', '/')) } else { $logo.ParentNode.RemoveChild($logo) | Out-Null }
$nodes = $xml.GetElementsByTagName('text')
$nodes.Item(0).InnerText = %s
$nodes.Item(1).InnerText = %s
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)`,
		psQuote(s.app+".Desktop"), psQuote(s.app), psQuote(s.icon), psQuote(title), psQuote(n.Body))
	cmd := exec.CommandContext(ctx, s.bin, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeUTF16(script))
	// The server often runs without a console: keep a window from
	// flashing for each notification.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell notify: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// encodeUTF16 makes the UTF-16LE base64 -EncodedCommand expects, which
// sidesteps every quoting problem in the notification text.
func encodeUTF16(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
