package worker

import "fmt"

// GenerateManifest creates an OTA manifest.plist for iOS installation.
func GenerateManifest(baseURL, jobID string) string {
	ipaURL := fmt.Sprintf("%s/artifacts/%s/signed.ipa", baseURL, jobID)
	imageURL := fmt.Sprintf("%s/artifacts/%s/icon.png", baseURL, jobID)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>items</key>
	<array>
		<dict>
			<key>assets</key>
			<array>
				<dict>
					<key>kind</key>
					<string>software-package</string>
					<key>url</key>
					<string>%s</string>
				</dict>
				<dict>
					<key>kind</key>
					<string>display-image</string>
					<key>needs-shine</key>
					<false/>
					<key>url</key>
					<string>%s</string>
				</dict>
			</array>
			<key>metadata</key>
			<dict>
				<key>bundle-identifier</key>
				<string>com.signy.signed</string>
				<key>bundle-version</key>
				<string>1.0</string>
				<key>kind</key>
				<string>software</string>
				<key>title</key>
				<string>Signed IPA (%s)</string>
			</dict>
		</dict>
	</array>
</dict>
</plist>`, ipaURL, imageURL, jobID)
}
