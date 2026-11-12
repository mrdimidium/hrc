Removal
=======

Removal stops active jobs. Decide which configuration, secrets, and data to
retain before deleting files. To update an existing installation instead, see
`Upgrading <./upgrading.rst>`__.

Contents
--------

- `Ansible <#ansible>`__
- `Manual removal <#manual-removal>`__

Ansible
-------

.. code-block:: yaml

   - hosts: runners
     become: true # Omit on Windows when connecting as an administrator.
     roles:
       - role: dimidiumlabs.hrc.remove
         vars:
           hrc_remove_data: false

``hrc_remove_data`` defaults to ``false``: remove the service, binaries and private
WinSW, retaining configuration, secrets, state, logs and the Unix account. Set
it to ``true`` to delete configuration, secrets, state and logs at the default
locations as well. Custom data paths supplied in ``hrc_config`` are not removed.
The Unix account is retained in either case.

Manual removal
--------------

Before deleting installed files, stop and disable or unregister the service.
Configuration, secrets, accounts and build data can be retained.

.. tabs::
   :group: host-platform

   Windows
   ~~~~~~~

   Run PowerShell as Administrator:

   .. code-block:: powershell

      & "$env:ProgramFiles\hrc\hrc-service.exe" stopwait
      & "$env:ProgramFiles\hrc\hrc-service.exe" uninstall

   macOS
   ~~~~~

   .. code-block:: sh

      sudo launchctl bootout system/io.dimidiumlabs.hrc
      sudo launchctl disable system/io.dimidiumlabs.hrc

   Omit ``bootout`` if the service is already unloaded.

   Linux / Systemd
   ~~~~~~~~~~~~~~~

   .. code-block:: sh

      sudo systemctl disable --now hrc.service

   After deleting the unit, run ``sudo systemctl daemon-reload``.

   Linux / OpenRC
   ~~~~~~~~~~~~~~

   .. code-block:: sh

      sudo rc-service hrc stop
      sudo rc-update del hrc default

   Also remove the service from any other runlevels where it was enabled.
