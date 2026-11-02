package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// xmlValue escapes a user-controlled path or environment value for a launchd plist.
func xmlValue(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

// launchAgent describes an rmt service without inheriting unrelated shell secrets.
func launchAgent(binary, path, configHome, logPath string) string {
	configEnv := ""
	if configHome != "" {
		configEnv = "<key>XDG_CONFIG_HOME</key><string>" + xmlValue(configHome) + "</string>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.rmt.host</string>
<key>ProgramArguments</key><array><string>%s</string><string>serve</string></array>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string>%s</dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlValue(binary), xmlValue(path), configEnv, xmlValue(logPath))
}
