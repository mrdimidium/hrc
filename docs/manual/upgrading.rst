Upgrading
=========

Schedule an interruption: upgrades restart the controller, cancel active jobs,
and remove slots, including idle reusable ones. Keep configuration and secrets;
do not repeat the first-time configuration examples.

For a new host, follow `Installation <./installation.rst>`__ instead.
To uninstall hrc, see `Removal <./removal.rst>`__.

Contents
--------

- `Ansible <#ansible>`__
- `Release archives <#release-archives>`__

Ansible
-------

Install a newer collection using the `collection download and verification
procedure <./installation.rst#ansible-collection>`__, then apply your existing
playbook. The collection pins the binary release; there is no independent
binary version override. An unchanged apply does not restart the service.

Release archives
----------------

`Download, verify, and extract <./installation.rst#download>`__ a new release,
then run the commands below from its extracted directory.

.. tabs::
   :group: host-platform

   Windows
   ~~~~~~~

   Run PowerShell as Administrator:

   .. code-block:: powershell

      $installRoot = "$env:ProgramFiles\hrc"
      $dataRoot = "$env:ProgramData\hrc"
      & "$installRoot\hrc-service.exe" stopwait
      Remove-Item "$installRoot\lib\hrc" -Recurse -Force

   Repeat the `program file and WinSW XML copy commands
   <./installation.rst#installation-1>`__, then ``Start-Service hrc``.
   Do not recreate directories or configuration, or register WinSW again.

   macOS
   ~~~~~

   Unload the service and remove the old runtime:

   .. code-block:: sh

      sudo ./scripts/preinstall.sh
      sudo rm -rf /usr/local/lib/hrc

   Repeat the `file and launchd definition commands
   <./installation.rst#installation-1>`__, then start:

   .. code-block:: sh

      sudo launchctl bootstrap system /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist

   Linux / Systemd
   ~~~~~~~~~~~~~~~

   The preinstall hook stops the service, retaining enablement. Remove the old
   runtime:

   .. code-block:: sh

      sudo ./scripts/preinstall.sh
      sudo rm -rf /usr/local/lib/hrc

   Repeat the `file and service installation commands
   <./installation.rst#installation-1>`__, then start:

   .. code-block:: sh

      sudo systemctl start hrc.service

   Linux / OpenRC
   ~~~~~~~~~~~~~~

   The preinstall hook stops the service, retaining enablement. Remove the old
   runtime:

   .. code-block:: sh

      sudo ./scripts/preinstall.sh
      sudo rm -rf /usr/local/lib/hrc

   Repeat the `file and service installation commands
   <./installation.rst#installation-1>`__, then start:

   .. code-block:: sh

      sudo rc-service hrc start

For other Linux init systems, stop and start the controller yourself.
Use `Operation <./operation.rst>`__ to check service status and logs afterward.
