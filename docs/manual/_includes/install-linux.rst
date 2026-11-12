The preinstall hook prepares the account and directories and stops an existing
systemd/OpenRC service.

.. code-block:: sh

   sudo ./scripts/preinstall.sh
   sudo install -d -m 0755 -o root -g root /usr/local/bin /usr/local/lib/hrc
   sudo install -m 0755 -o root -g root hrc hrc-worker /usr/local/bin/
   sudo cp -R runner/. /usr/local/lib/hrc/
   sudo chown -R root:root /usr/local/lib/hrc
   sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc
