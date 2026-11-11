Installation
============

Contents
--------

- `Ansible collection <#ansible-collection>`__
- `Manual installation <#manual-installation>`__

  - `Prerequisites <#prerequisites>`__
  - `Download <#download>`__
  - `Installation <#installation-1>`__
  - `Upgrading <#upgrading>`__
  - `Removal <#removal>`__

Ansible collection
------------------

The ``dimidiumlabs.hrc`` collection provides two roles: ``install`` and ``remove``.
Each collection archive pins the filenames and SHA-256 hashes of one hrc
release's binaries. On the Ansible controller (Linux or macOS), use a fresh
working directory with ``curl`` and ``jq`` installed to download and install the
collection from the latest release:

.. code-block:: sh

   version=$(curl -fsL https://pkg.dimidiumlabs.io/hrc/channels/stable.json | jq -er '.version')
   release="https://pkg.dimidiumlabs.io/hrc/builds/$version"
   archive="ansible-collection-$version.tar.gz"

   curl -fLO "$release/$archive" -O "$release/SHASUMS256"
   case $(uname -s) in
       Darwin) shasum -a 256 -c --ignore-missing SHASUMS256 ;;
       *) sha256sum -c --ignore-missing SHASUMS256 ;;
   esac

   ansible-galaxy collection install "./$archive"


Stop if download or checksum verification fails. To install a specific release,
set ``version`` to its version number instead of reading it from ``manifest.json``.

An HTTPS URL to the archive, including a mirror or S3, can also be used as a
``type: url`` entry in a collection ``requirements.yml``. Verify the collection
itself before installation; the embedded hashes verify the binaries, not the
collection.

Requires Ansible Core 2.18+, ``ansible.windows`` 3+ for Windows, gathered host
facts, and administrative access. Linux supports systemd and OpenRC with glibc;
install the `native Worker dependencies <#prerequisites>`__ first. macOS needs
Python for Ansible. Windows supports SSH/PowerShell or WinRM. Build tools,
Docker, OS updates and SSH configuration are not managed by these roles.

Install and upgrade
~~~~~~~~~~~~~~~~~~~

.. code-block:: yaml

   - hosts: runners
     become: true # Omit on Windows when connecting as an administrator.
     roles:
       - role: dimidiumlabs.hrc.install
         vars:
           hrc_config:
             id: build-01
             quota: { cpu: 2, memory: 4GiB }
             groups:
               - url: https://github.com/your-org/your-repo
                 name: builds
                 labels: [linux, X64]
                 pat: { type: file, value: /etc/hrc/secrets/github-token }
           hrc_secrets:
             github-token: "{{ vault_github_token }}"


Public variables:

.. list-table::
   :header-rows: 1

   * - Variable
     - Default
     - Meaning
   * - ``hrc_config``
     - Required
     - Mapping written as hrc's JSON configuration. Must contain ``id`` and ``groups``.
   * - ``hrc_secrets``
     - ``{}``
     - Mapping of simple filenames to secret contents, installed under the secrets directory.
   * - ``hrc_artifact_source``
     - URL supplied by release CI
     - Base HTTP(S) URL or absolute directory **on the Ansible controller** containing the release archives.


The role supplies ``pathWorker``, ``dirRunner``, ``dirBuilds`` and ``dirLogs`` defaults;
``hrc_config`` can override them. Other fields are passed through unchanged; see
`configuration and operation <./usage.rst>`__. Secret values and configuration are
not logged or diffed. Omitted secret files are retained: revoke credentials
explicitly. Jobs run as the controller's service account and can read its
credentials; use only trusted jobs.

The default artifact source is supplied by release CI and embedded in the
collection, not inferred from the version.

To vendor binaries, copy the release tarballs without renaming them and set
``hrc_artifact_source`` to their directory. Changing the source never changes or
disables the pinned checksum checks. WinSW is bundled privately on Windows; the
role does not use or change a global WinSW installation.

Upgrade by installing a newer collection. There is no independent binary version
override. The role enables and starts the service; installation or configuration
changes restart it, **terminating active jobs and cleaning up their
workspaces**. Schedule upgrades accordingly. An unchanged apply does not restart
it. ``--check`` does not install or stop anything; configuration tasks are skipped
if installation is needed.

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


Remove
~~~~~~

.. code-block:: yaml

   - hosts: runners
     become: true
     roles:
       - role: dimidiumlabs.hrc.remove
         vars:
           hrc_remove_data: false


``hrc_remove_data`` defaults to ``false``: remove the service, binaries and private
WinSW, retaining configuration, secrets, state, logs and the Unix account. Set
it to ``true`` to delete configuration, secrets, state and logs at the default
locations as well. Custom data paths supplied in ``hrc_config`` are not removed.
The Unix account is retained in either case. Removal terminates active jobs.

Manual installation
-------------------

Use a fresh working directory and the same shell session. After installation,
follow `configuration and operation <./usage.rst>`__ to configure and start the
service. For an existing installation, follow the
`upgrade procedure <#upgrading>`__.

Prerequisites
~~~~~~~~~~~~~

Release archives include the Worker's .NET 8 runtime and Node.js 20/24. A
separate .NET runtime, SDK or Node.js installation is not required.

Install Git and any other tools needed by your workflows separately. Docker is
needed only for Linux container jobs, service containers and container actions.

Linux archives require glibc; Alpine/musl hosts are not supported.

**Windows**

Run PowerShell as Administrator. ``tar`` is included with current Windows
versions. Download WinSW 2.x (x64 example):

.. code-block:: powershell

   New-Item -ItemType Directory -Force C:\Downloads | Out-Null
   Invoke-WebRequest 'https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe' -OutFile C:\Downloads\WinSW-x64.exe


Verify the downloaded WinSW executable before using it.

**macOS**

.. code-block:: sh

   # macOS 13.5 or later: no additional packages required.


**Ubuntu / Debian**

.. code-block:: sh

   sudo apt-get update
   sudo apt-get install -y ca-certificates curl tar gzip libc6 libgcc-s1 \
       libstdc++6 libkrb5-3 libgssapi-krb5-2 tzdata zlib1g

   . /etc/os-release
   if [ "$ID" = ubuntu ] && [ "$VERSION_ID" = 22.04 ]; then
       sudo apt-get install -y libicu70 libssl3 liblttng-ust1
   elif [ "$ID" = ubuntu ] && [ "$VERSION_ID" = 24.04 ]; then
       sudo apt-get install -y libicu74 libssl3t64 liblttng-ust1t64
   elif [ "$ID" = debian ] && [ "$VERSION_ID" = 12 ]; then
       sudo apt-get install -y libicu72 libssl3 liblttng-ust1
   elif [ "$ID" = debian ] && [ "$VERSION_ID" = 13 ]; then
       sudo apt-get install -y libicu76 libssl3t64 liblttng-ust1t64
   else
       printf 'Package versions for %s %s are not listed here.\n' "$ID" "$VERSION_ID" >&2
   fi


**RHEL / AlmaLinux / Rocky Linux / CentOS Stream / Fedora**

.. code-block:: sh

   sudo dnf install -y ca-certificates curl tar gzip glibc libgcc libstdc++ \
       krb5-libs tzdata zlib openssl-libs libicu lttng-ust


**openSUSE Leap 16**

.. code-block:: sh

   sudo zypper -n install ca-certificates curl tar gzip glibc libgcc_s1 \
       libstdc++6 krb5 timezone zlib libopenssl3 libicu lttng-ust


Download
~~~~~~~~

**macOS / Linux / Windows (Git Bash)**

Requires ``curl`` and ``jq``.

.. code-block:: sh

   case $(uname -s) in Darwin) os=darwin ;; Linux) os=linux ;; MINGW*|MSYS*) os=windows ;; esac
   case ${PROCESSOR_ARCHITEW6432:-${PROCESSOR_ARCHITECTURE:-$(uname -m)}} in x86_64|AMD64) arch=amd64 ;; arm64|aarch64|ARM64) arch=arm64 ;; esac

   version=$(curl -fsL https://pkg.dimidiumlabs.io/hrc/channels/stable.json | jq -er '.version')
   release="https://pkg.dimidiumlabs.io/hrc/builds/$version"
   archive="hrc-$arch-$os-$version.tar.gz"
   curl -fLO "$release/$archive" -O "$release/SHASUMS256"
   case $os in darwin) shasum -a 256 -c --ignore-missing SHASUMS256 ;; *) sha256sum -c --ignore-missing SHASUMS256 ;; esac


Stop if checksum verification fails.

**Windows (PowerShell as Administrator)**

.. code-block:: powershell

   $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
   $arch = switch ($machine) { AMD64 { 'amd64' } ARM64 { 'arm64' } }

   $version = (Invoke-RestMethod 'https://pkg.dimidiumlabs.io/hrc/channels/stable.json').version
   $release = "https://pkg.dimidiumlabs.io/hrc/builds/$version"
   $archive = "hrc-$arch-windows-$version.tar.gz"
   Invoke-WebRequest "$release/SHASUMS256" -OutFile SHASUMS256
   Invoke-WebRequest "$release/$archive" -OutFile $archive
   Get-FileHash $archive -Algorithm SHA256


Compare the hash with the archive's entry in ``SHASUMS256`` before continuing.

**Verify the checksum signature**

Releases produced by the signing pipeline include ``SHASUMS256.asc``, a
detached OpenPGP signature of ``SHASUMS256``. Import the Dimidium Labs public
key bundle from ``https://pkg.dimidiumlabs.io/packages.gpg`` into GPG and verify
its fingerprint through an independent trusted source before trusting it.
Then, in macOS / Linux / Git Bash:

.. code-block:: sh

   curl -fLO "$release/SHASUMS256.asc"
   gpg --verify SHASUMS256.asc SHASUMS256


Stop if signature verification fails or the signing key is not trusted. A good
signature from an unknown key does not authenticate the publisher. Verify the
archive's checksum against this authenticated list before extracting it.
Older releases may not have a signature.

Each signed release also includes ``manifest.json`` (schema version 1), containing
the product, version, full source commit, and an artifact list with filenames,
sizes in bytes and SHA-256 hashes. Filenames are relative to the directory
containing the manifest, so the same manifest works on either mirror. Binary archives additionally
identify their OS and architecture. ``manifest.json`` is covered
by the signed checksum list; the list and its signature are not included in
the manifest, avoiding a circular dependency.

The public mirror stores every build at
``https://pkg.dimidiumlabs.io/hrc/builds/{version}/``, using the full version,
including dev suffixes and build metadata. Files at these URLs are immutable.
``channels/stable.json`` and ``channels/dev.json`` are mutable copies of the chosen
build's ``manifest.json``, updated only after publication to S3 and GitHub
completes. GitHub Releases contain the same files, but the GitHub ``dev`` release
retains only the current build. The Ansible collection uses the immutable
mirror URLs by default.

**Optional: verify build provenance**

With `GitHub CLI <https://cli.github.com/>`__, in either shell:

.. code-block:: sh

   gh attestation verify "$archive" -R mrdimidium/hrc


**Extract the archive**

macOS / Linux / Git Bash:

.. code-block:: sh

   mkdir hrc-install
   cd hrc-install
   tar -xzf "../$archive"


PowerShell:

.. code-block:: powershell

   New-Item -ItemType Directory hrc-install
   Set-Location hrc-install
   tar -xzf "..\$archive"


After extraction in Git Bash, switch to PowerShell as Administrator in the
``hrc-install`` directory.

.. _installation-1:

Installation
~~~~~~~~~~~~

**Linux**

The preinstall hook prepares the account and directories and stops an existing
systemd/OpenRC service. Other init systems require manual service setup.

.. code-block:: sh

   sudo ./scripts/preinstall.sh
   sudo install -d -m 0755 -o root -g root /usr/local/bin /usr/local/lib/hrc
   sudo install -m 0755 -o root -g root hrc hrc-worker /usr/local/bin/
   sudo cp -R runner/. /usr/local/lib/hrc/
   sudo chown -R root:root /usr/local/lib/hrc
   sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc


Install the definition for your init system. For systemd:

.. code-block:: sh

   sudo install -m 0644 -o root -g root services/hrc.systemd /etc/systemd/system/hrc.service
   sudo systemctl daemon-reload


For OpenRC:

.. code-block:: sh

   sudo install -m 0755 -o root -g root services/hrc.alpine /etc/init.d/hrc


**macOS**

The preinstall hook prepares the account and directories and unloads an existing
launchd service.

.. code-block:: sh

   sudo ./scripts/preinstall.sh
   sudo install -d -m 0755 -o root -g wheel /usr/local/bin /usr/local/lib/hrc
   sudo install -m 0755 -o root -g wheel hrc hrc-worker /usr/local/bin/
   sudo cp -R runner/. /usr/local/lib/hrc/
   sudo chown -R root:wheel /usr/local/lib/hrc
   sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc
   sudo install -m 0644 -o root -g wheel services/io.dimidiumlabs.hrc.plist /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist
   sudo plutil -lint /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist


**Windows (PowerShell as Administrator)**

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


Continue with `configuration and operation <./usage.rst>`__.

Upgrading
~~~~~~~~~

`Download, verify, and extract <#download>`__ a new release, then run the commands
below from the extracted directory. Keep existing configuration, secrets and
data; do not repeat the configuration examples in the
`usage guide <./usage.rst#configuration>`__.

**Linux**

The preinstall hook stops the systemd/OpenRC service, retaining enablement. For
other init systems, stop the controller yourself. Remove the old runtime:

.. code-block:: sh

   sudo ./scripts/preinstall.sh
   sudo rm -rf /usr/local/lib/hrc


Repeat the file and service definition commands from
`installation <#installation-1>`__, then run ``sudo systemctl start hrc.service`` or
``sudo rc-service hrc start``.

**macOS**

Unload the service and remove the old runtime:

.. code-block:: sh

   sudo ./scripts/preinstall.sh
   sudo rm -rf /usr/local/lib/hrc


Repeat the file and launchd definition commands from
`installation <#installation-1>`__, then start:

.. code-block:: sh

   sudo launchctl bootstrap system /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist


**Windows (PowerShell as Administrator)**

Set the installation paths, stop the service and remove the old runtime:

.. code-block:: powershell

   $installRoot = "$env:ProgramFiles\hrc"
   $dataRoot = "$env:ProgramData\hrc"
   & "$installRoot\hrc-service.exe" stopwait
   Remove-Item "$installRoot\lib\hrc" -Recurse -Force


Repeat the program file and WinSW XML copy commands from
`installation <#installation-1>`__, then ``Start-Service hrc``. Do not recreate the
directories or configuration, or register WinSW again.

Removal
~~~~~~~

Before deleting installed files, stop and disable or unregister the service.
Configuration, secrets, accounts and build data can be retained.

- **systemd:** ``sudo systemctl disable --now hrc.service``. After deleting the
  unit, run ``sudo systemctl daemon-reload``.
- **OpenRC:** ``sudo rc-service hrc stop`` and ``sudo rc-update del hrc default``
  (and remove it from any other runlevels).
- **launchd:** ``sudo launchctl bootout system/io.dimidiumlabs.hrc`` (omit if already
  unloaded) and ``sudo launchctl disable system/io.dimidiumlabs.hrc``.
- **Windows (PowerShell as Administrator):**
  ``& "$env:ProgramFiles\hrc\hrc-service.exe" stopwait`` and
  ``& "$env:ProgramFiles\hrc\hrc-service.exe" uninstall``.
