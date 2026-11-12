Installation
============

First `prepare the host <./host-preparation.rst>`__ and
`set up GitHub access <./github-auth.rst>`__. Ansible is recommended;
release archives are the most portable alternative. Both routes end with a
configured service ready for `workflow configuration <./workflows.rst>`__.
For an existing installation, see `Upgrading <./upgrading.rst>`__ or
`Removal <./removal.rst>`__.

Contents
--------

- `Ansible collection <#ansible-collection>`__
- `Manual installation <#manual-installation>`__

  - `Download <#download>`__
  - `Install files and services <#installation-1>`__
  - `Configure and start <#configure-and-start>`__

Ansible collection
------------------

The ``dimidiumlabs.hrc`` collection provides two roles: ``install`` and ``remove``.
Each collection archive pins the filenames and SHA-256 hashes of one hrc
release's binaries. On the Ansible controller (Linux or macOS), use a fresh
working directory with ``curl`` and ``jq`` installed to download and install the
collection from the latest release:

.. code-block:: sh

   version=$(curl -fsL https://pkg.dimidiumlabs.io/hrc/index.json | jq -er '.channels.stable')
   release="https://pkg.dimidiumlabs.io/hrc/builds/$version"
   archive="ansible-collection-$version.tar.gz"

   curl -fLO "$release/$archive" -O "$release/SHASUMS256"
   case $(uname -s) in
       Darwin) shasum -a 256 -c --ignore-missing SHASUMS256 ;;
       *) sha256sum -c --ignore-missing SHASUMS256 ;;
   esac

Stop if download or checksum verification fails. To install a specific release,
set ``version`` to its version number instead of reading it from ``index.json``.
Verify the `checksum signature <#verify-the-checksum-signature>`__ before
installing the collection; the embedded checksums verify the binaries, not
the collection itself. After verification, install the collection:

.. code-block:: sh

   ansible-galaxy collection install "./$archive"

An HTTPS URL to the archive, including a mirror or S3, can also be used as a
``type: url`` entry in a collection ``requirements.yml``. Verify the archive
before handing it to automated installation.

Requires Ansible Core 2.18+, ``ansible.windows`` 3+ for Windows, gathered host
facts, and administrative access. Check `host preparation
<./host-preparation.rst>`__ for dependencies and management connections.
Build tools, Docker, OS updates, and SSH configuration are not managed by
these roles.

Configure and deploy
~~~~~~~~~~~~~~~~~~~~

For a Linux host using a PAT, save this as ``hrc.yml`` and replace the repository
URL. Prepare the token using `GitHub access <./github-auth.rst>`__.

.. code-block:: yaml

   - hosts: all
     become: true
     vars_prompt:
       - name: github_token
         prompt: GitHub PAT
         private: true
     roles:
       - role: dimidiumlabs.hrc.install
         vars:
           hrc_config:
             id: build-01
             quota: { cpu: 1, memory: 1GiB }
             groups:
               - url: https://github.com/YOUR-OWNER/YOUR-REPO
                 name: hrc-builds
                 pat: { type: file, value: /etc/hrc/secrets/github-token }
           hrc_secrets:
             github-token: "{{ github_token }}"

Run it with your host and SSH user (keep the comma after the hostname):

.. code-block:: sh

   ansible-playbook -i 'YOUR-HOST,' -u YOUR-SSH-USER --ask-become-pass hrc.yml

The prompt reads the PAT without putting it in the playbook. ``hrc_secrets``
writes its value to ``/etc/hrc/secrets/github-token``; ``pat`` tells hrc to read
that same file. For unattended use, supply the secret through Ansible Vault
or your existing secret management instead of an interactive prompt.

On macOS, use ``/usr/local/etc/hrc/secrets/github-token`` in ``pat.value``.
On Windows, use ``C:/ProgramData/hrc/secrets/github-token``, connect as an
administrator, and omit ``become`` and ``--ask-become-pass``. Configure the
Ansible connection for the host's SSH/PowerShell or WinRM transport.
For GitHub App authentication, substitute the ``app`` mapping and private key
described in `GitHub access <./github-auth.rst#github-app>`__.
Choose a distinct host ID and quotas appropriate for each machine.

The role supplies the Worker and resource paths, writes configuration and
secrets without logging their values, and enables and starts the service.
Installation or configuration changes restart it, cancelling active jobs and
cleaning up workspaces. ``--check`` does not install or stop anything;
configuration tasks are skipped if installation is needed.

See `Ansible variables <../configuration.rst#ansible-variables>`__ for overrides
and vendored binaries. WinSW is bundled privately on Windows; the role does not
use or change a global WinSW installation.

Default target paths:

.. list-table::
   :header-rows: 1

   * - OS
     - Config/secrets parent
     - State
     - Logs
   * - Linux
     - ``/etc/hrc``
     - ``/var/lib/hrc``
     - ``/var/log/hrc``
   * - macOS
     - ``/usr/local/etc/hrc``
     - ``/usr/local/var/hrc``
     - ``/Library/Logs/hrc``
   * - Windows
     - ``C:\ProgramData\hrc``
     - ``C:\ProgramData\hrc\state``
     - ``C:\ProgramData\hrc\logs``


The host is now ready for `its first workflow <./workflows.rst>`__.
For later changes, see `Operation <./operation.rst>`__.

Manual installation
-------------------

Use a fresh working directory and the same shell session. Download and verify
the archive, install its files, then configure and start the service below.

Download
~~~~~~~~

.. tabs::
   :group: operating-system

   Windows
   ~~~~~~~

   On Windows, run PowerShell as Administrator:

   .. code-block:: powershell

      $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
      $arch = switch ($machine) { AMD64 { 'amd64' } ARM64 { 'arm64' } }

      $version = (Invoke-RestMethod 'https://pkg.dimidiumlabs.io/hrc/index.json').channels.stable
      $release = "https://pkg.dimidiumlabs.io/hrc/builds/$version"
      $archive = "hrc-$arch-windows-$version.tar.gz"
      Invoke-WebRequest "$release/SHASUMS256" -OutFile SHASUMS256
      Invoke-WebRequest "$release/$archive" -OutFile $archive
      Get-FileHash $archive -Algorithm SHA256

   Compare the hash with the archive's entry in ``SHASUMS256`` before continuing.

   macOS
   ~~~~~

   Requires ``curl`` and ``jq``:

   .. code-block:: sh

      case $(uname -m) in x86_64) arch=amd64 ;; arm64) arch=arm64 ;; esac
      version=$(curl -fsL https://pkg.dimidiumlabs.io/hrc/index.json | jq -er '.channels.stable')
      release="https://pkg.dimidiumlabs.io/hrc/builds/$version"
      archive="hrc-$arch-darwin-$version.tar.gz"
      curl -fLO "$release/$archive" -O "$release/SHASUMS256"
      shasum -a 256 -c --ignore-missing SHASUMS256

   Linux
   ~~~~~

   Requires ``curl`` and ``jq``:

   .. code-block:: sh

      case $(uname -m) in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
      version=$(curl -fsL https://pkg.dimidiumlabs.io/hrc/index.json | jq -er '.channels.stable')
      release="https://pkg.dimidiumlabs.io/hrc/builds/$version"
      archive="hrc-$arch-linux-$version.tar.gz"
      curl -fLO "$release/$archive" -O "$release/SHASUMS256"
      sha256sum -c --ignore-missing SHASUMS256

Stop if download or checksum verification fails.

.. _verify-the-checksum-signature:

**Verify the checksum signature**

Import the `signing key <https://pkg.dimidiumlabs.io/packages.gpg>`__ into GPG
and independently confirm its `fingerprint <../index.rst#package-signing>`__.
Verify the signature in the same shell used for the download:

.. tabs::
   :group: command-shell
   :label: Command shell

   PowerShell
   ~~~~~~~~~~

   .. code-block:: powershell

      Invoke-WebRequest "$release/SHASUMS256.asc" -OutFile SHASUMS256.asc
      gpg --verify SHASUMS256.asc SHASUMS256

   POSIX shell
   ~~~~~~~~~~~

   .. code-block:: sh

      curl -fLO "$release/SHASUMS256.asc"
      gpg --verify SHASUMS256.asc SHASUMS256


Stop on a bad signature or an unverified signing key. Before extracting, match
the archive's checksum against the signed ``SHASUMS256``.

**Optional: verify build provenance**

With `GitHub CLI <https://cli.github.com/>`__, in either shell:

.. code-block:: sh

   gh attestation verify "$archive" -R mrdimidium/hrc


**Extract the archive**

.. tabs::
   :group: command-shell
   :label: Command shell

   PowerShell
   ~~~~~~~~~~

   .. code-block:: powershell

      New-Item -ItemType Directory hrc-install
      Set-Location hrc-install
      tar -xzf "..\$archive"

   POSIX shell
   ~~~~~~~~~~~

   .. code-block:: sh

      mkdir hrc-install
      cd hrc-install
      tar -xzf "../$archive"


.. _installation-1:

Installation
~~~~~~~~~~~~

.. tabs::
   :group: host-platform

   Windows
   ~~~~~~~

   Run PowerShell as Administrator. Download WinSW 2.x (x64 example) and verify
   the executable before using it:

   .. code-block:: powershell

      New-Item -ItemType Directory -Force C:\Downloads | Out-Null
      Invoke-WebRequest 'https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe' -OutFile C:\Downloads\WinSW-x64.exe

   Use an appropriate WinSW build for your host. The commands below assume the
   x64 filename above.

   .. code-block:: powershell

      $installRoot = "$env:ProgramFiles\hrc"
      $dataRoot = "$env:ProgramData\hrc"

   Create directories on the first installation:

   .. code-block:: powershell

      New-Item -ItemType Directory -Path $installRoot, "$installRoot\lib", $dataRoot, "$dataRoot\secrets", "$dataRoot\state", "$dataRoot\logs" | Out-Null

   The service uses ``LocalService``: read/execute access, with write access only to
   state and logs. SYSTEM and Administrators retain full access.

   .. code-block:: powershell

      icacls $installRoot /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)RX'
      icacls $dataRoot /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)RX'
      icacls "$dataRoot\state" /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)M'
      icacls "$dataRoot\logs" /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)M'

   Copy the program files and fill the WinSW paths:

   .. code-block:: powershell

      Copy-Item .\hrc.exe, .\hrc-worker.exe -Destination $installRoot -Force
      Copy-Item .\runner -Destination "$installRoot\lib\hrc" -Recurse

      [xml] $xml = Get-Content .\services\hrc.winsw.xml -Raw
      $xml.service.executable = [string]"$installRoot\hrc.exe"
      $xml.service.arguments = $xml.service.arguments.Replace('%SERVICE_CONFIG%', "$dataRoot\hrc.json")
      $xml.service.workingdirectory = [string]"$dataRoot\state"
      $xml.service.logpath = [string]"$dataRoot\logs"
      $xml.Save("$installRoot\hrc-service.xml")

   Register WinSW on the first installation only:

   .. code-block:: powershell

      Copy-Item C:\Downloads\WinSW-x64.exe -Destination "$installRoot\hrc-service.exe"
      & "$installRoot\hrc-service.exe" install

   macOS
   ~~~~~

   The preinstall hook prepares the account and directories and unloads an
   existing launchd service.

   .. code-block:: sh

      sudo ./scripts/preinstall.sh
      sudo install -d -m 0755 -o root -g wheel /usr/local/bin /usr/local/lib/hrc
      sudo install -m 0755 -o root -g wheel hrc hrc-worker /usr/local/bin/
      sudo cp -R runner/. /usr/local/lib/hrc/
      sudo chown -R root:wheel /usr/local/lib/hrc
      sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc
      sudo install -m 0644 -o root -g wheel services/io.dimidiumlabs.hrc.plist /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist
      sudo plutil -lint /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist

   Linux / Systemd
   ~~~~~~~~~~~~~~~

   .. include:: _includes/install-linux.rst

   .. code-block:: sh

      sudo install -m 0644 -o root -g root services/hrc.systemd /etc/systemd/system/hrc.service
      sudo systemctl daemon-reload

   Linux / OpenRC
   ~~~~~~~~~~~~~~

   .. include:: _includes/install-linux.rst

   .. code-block:: sh

      sudo install -m 0755 -o root -g root services/hrc.alpine /etc/init.d/hrc

Other Linux init systems require manual service setup.


Configure and start
~~~~~~~~~~~~~~~~~~~

First installation only: replace the host ID, repository URL, and token source
path. These examples use a file-based PAT. For GitHub App authentication,
substitute the ``app`` mapping and private key from
`GitHub access <./github-auth.rst#github-app>`__.

.. tabs::
   :group: command-shell
   :label: Command shell

   PowerShell
   ~~~~~~~~~~

   On Windows, run PowerShell as Administrator. Set these paths in each new
   session. Write UTF-8 without a BOM:

   .. code-block:: powershell

      $installRoot = "$env:ProgramFiles\hrc"
      $dataRoot = "$env:ProgramData\hrc"
      $config = @{
          id = 'build-01'
          quota = @{ cpu = 1; memory = '1GiB' }
          pathWorker = "$installRoot\hrc-worker.exe"
          dirRunner = "$installRoot\lib\hrc"
          dirBuilds = 'jobs'
          dirLogs = "$dataRoot\logs"
          groups = @(
              @{
                  url = 'https://github.com/YOUR-OWNER/YOUR-REPO'
                  name = 'hrc-builds'
                  pat = @{ type = 'file'; value = "$dataRoot\secrets\github-token" }
              }
          )
      } | ConvertTo-Json -Depth 10
      [IO.File]::WriteAllText("$dataRoot\hrc.json", $config + "`n", (New-Object Text.UTF8Encoding($false)))

      Copy-Item C:\path\to\github-token -Destination "$dataRoot\secrets\github-token"
      icacls "$dataRoot\secrets\github-token" /reset

   POSIX shell
   ~~~~~~~~~~~

   On macOS or Linux:

   .. code-block:: sh

      case $(uname -s) in Darwin) config_dir=/usr/local/etc/hrc; log_dir=/Library/Logs/hrc ;; Linux) config_dir=/etc/hrc; log_dir=/var/log/hrc ;; esac

      sudo tee "$config_dir/hrc.json" > /dev/null <<EOF
      {
        "id": "build-01",
        "quota": {"cpu": 1, "memory": "1GiB"},
        "pathWorker": "/usr/local/bin/hrc-worker",
        "dirRunner": "/usr/local/lib/hrc",
        "dirBuilds": "jobs",
        "dirLogs": "$log_dir",
        "groups": [{
          "url": "https://github.com/YOUR-OWNER/YOUR-REPO",
          "name": "hrc-builds",
          "pat": {"type": "file", "value": "$config_dir/secrets/github-token"}
        }]
      }
      EOF
      sudo chown root:hrc "$config_dir/hrc.json"
      sudo chmod 0640 "$config_dir/hrc.json"
      sudo install -m 0640 -o root -g hrc /path/to/github-token "$config_dir/secrets/github-token"

Choose quotas appropriate for your host and builds. See the
`configuration reference <../configuration.rst>`__ for other fields and defaults.

Start the service and check its status using the
`commands for your service manager <./operation.rst#service-management>`__.
Next: `configure a workflow and run a first build <./workflows.rst>`__.
