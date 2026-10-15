<?xml version="1.0" encoding="UTF-8"?>
<!-- SPDX-FileCopyrightText: 2026 Nikolay Govorov -->
<!-- SPDX-License-Identifier: BSD-3-Clause -->
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key>
  <string>@LABEL@</string>

  <key>UserName</key>
  <string>@USER@</string>

  <key>GroupName</key>
  <string>@GROUP@</string>

  <key>ProgramArguments</key>
  <array>
@ARGUMENTS@
  </array>

  <key>WorkingDirectory</key>
  <string>@STATE@</string>

  <!-- Explicitly enable after configuring providers and secrets. -->
  <key>Disabled</key>
  <true/>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>

  <key>ThrottleInterval</key>
  <integer>10</integer>

  <key>Umask</key>
  <integer>63</integer>

  <key>ExitTimeOut</key>
  <integer>60</integer>

  <key>StandardOutPath</key>
  <string>@LOG@/stdout.log</string>

  <key>StandardErrorPath</key>
  <string>@LOG@/stderr.log</string>
</dict></plist>
