Operation
=========

Manage an installed host. For first-time setup, follow the
`Manual <./index.rst>`__. Configuration fields are described in the
`Configuration reference <../configuration.rst>`__.
For lifecycle changes, see `Upgrading <./upgrading.rst>`__ or
`Removal <./removal.rst>`__.

Contents
--------

- `Service management <#service-management>`__
- `Workspaces and cleanup <#workspaces-and-cleanup>`__
- `Logs <#logs>`__

Service management
------------------

Start only after configuring GitHub access and secrets. Restart after
configuration changes. Stopping or restarting the controller cancels active
jobs and cleans up their workspaces; it does not wait for jobs to finish.

.. tabs::
   :group: host-platform

   Windows
   ~~~~~~~

   Run PowerShell as Administrator:

   .. code-block:: powershell

      Start-Service hrc
      Get-Service hrc

   Stop: ``Stop-Service hrc``. Restart: ``Restart-Service hrc``.
   The manually installed WinSW service starts manually; use
   ``Set-Service hrc -StartupType Automatic`` if it should start at boot.
   The Ansible role enables automatic startup itself.

   macOS
   ~~~~~

   .. code-block:: sh

      sudo launchctl enable system/io.dimidiumlabs.hrc
      sudo launchctl bootstrap system /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist
      sudo launchctl print system/io.dimidiumlabs.hrc

   Stop: ``sudo launchctl bootout system/io.dimidiumlabs.hrc``. To restart, boot out
   and bootstrap again. If already unloaded, only bootstrap is needed.

   Linux / Systemd
   ~~~~~~~~~~~~~~~

   Enable at boot, start, and check status:

   .. code-block:: sh

      sudo systemctl enable --now hrc.service
      sudo systemctl status hrc.service

   Stop: ``sudo systemctl stop hrc.service``.
   Restart: ``sudo systemctl restart hrc.service``.

   Linux / OpenRC
   ~~~~~~~~~~~~~~

   .. code-block:: sh

      sudo rc-update add hrc default
      sudo rc-service hrc start
      sudo rc-service hrc status

   Stop: ``sudo rc-service hrc stop``. Restart: ``sudo rc-service hrc restart``.

Workspaces and cleanup
----------------------

Each runner slot owns a workspace and a process group. By default, both are
cleaned up after the runner finishes. With ``reuseDirs: true`` in a configured
group, workspaces and their remaining build processes can be reused across
jobs during one controller run. Each job still gets a new ephemeral runner
registration. Installed tools and caches outside the slot directories remain
on the host independently of this setting.

Graceful controller shutdown expires all slots, including idle reusable ones:
remaining slot processes are killed and slot directories are deleted. Job
diagnostics are stored separately under ``<dirLogs>/jobs/`` and are not removed
with the workspaces. Log retention is managed by the administrator.

On all three platforms, process cleanup uses the inherited
``RUNNER_TRACKING_GROUP`` environment variable and is best effort. Windows also
retains a Job Object as a crash-cleanup safety net. On Linux and macOS, cleanup
cannot run if the controller is killed with ``SIGKILL`` or crashes.

Logs
----

Controller output:

.. tabs::
   :group: host-platform

   Windows
   ~~~~~~~

   WinSW logs are stored in ``C:\ProgramData\hrc\logs``.

   macOS
   ~~~~~

   - ``/Library/Logs/hrc/stdout.log``
   - ``/Library/Logs/hrc/stderr.log``

   Linux / Systemd
   ~~~~~~~~~~~~~~~

   .. code-block:: sh

      sudo journalctl -u hrc.service -f

   Linux / OpenRC
   ~~~~~~~~~~~~~~

   - ``/var/log/hrc/stdout.log``
   - ``/var/log/hrc/stderr.log``

Job logs are stored under ``<dirLogs>/jobs/`` as configured in ``hrc.json``.
Retain logs from a failed job before removing them; deleting a workspace does
not delete its diagnostic logs.
