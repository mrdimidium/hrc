<?xml version="1.0" encoding="utf-8"?>
<!-- SPDX-FileCopyrightText: 2026 Nikolay Govorov -->
<!-- SPDX-License-Identifier: BSD-3-Clause -->
<service>
  <id>@NAME@</id>
  <name>@NAME@</name>
  <description>@DESCRIPTION@</description>
  <!-- The installer fills these paths for the selected installation directories. -->
  <executable>@EXECUTABLE@</executable>
  <arguments>@ARGUMENTS@</arguments>
  <workingdirectory>.</workingdirectory>
  <logpath>logs</logpath>
  <log mode="roll" />
  <startmode>Manual</startmode>
  <serviceaccount>
    <domain>NT AUTHORITY</domain>
    <user>LocalService</user>
  </serviceaccount>
  <onfailure action="restart" delay="10 sec" />
  <stoptimeout>60 sec</stoptimeout>
</service>
