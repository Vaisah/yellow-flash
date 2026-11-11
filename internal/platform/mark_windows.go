//go:build windows

package platform

import (
	"os"
	"strings"
)

func markFromInternet(path, from, referrer string) {
	clean := func(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }
	text := "[ZoneTransfer]\r\nZoneId=3\r\n"
	if referrer != "" {
		text += "ReferrerUrl=" + clean(referrer) + "\r\n"
	}
	if from != "" {
		text += "HostUrl=" + clean(from) + "\r\n"
	}
	_ = os.WriteFile(path+":Zone.Identifier", []byte(text), 0o644)
}
