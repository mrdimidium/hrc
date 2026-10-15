#!/sbin/openrc-run
# shellcheck shell=sh disable=SC2034
# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

description=@DESCRIPTION@

command=@EXECUTABLE@
command_args=@ARGUMENTS@
command_user=@OWNER@
directory=@STATE@
@REQUIRED_FILES@

supervisor="supervise-daemon"
respawn_delay=10
respawn_max=0
retry="TERM/60/KILL/5"
stopgroup="yes"
umask="0077"
output_log=@STDOUT@
error_log=@STDERR@

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath -d -o @OWNER@ -m 0700 @STATE@ || return 1
    checkpath -d -o @OWNER@ -m 0700 @LOG@ || return 1
}
