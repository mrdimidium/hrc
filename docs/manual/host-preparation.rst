Host preparation
================

Prepare the machine before installing hrc, regardless of whether you use
Ansible or release archives. hrc manages runners; it does not install your
build tools or configure the host for your workloads.

Contents
--------

- `Common requirements <#common-requirements>`__
- `Linux <#linux>`__
- `macOS <#macos>`__
- `Windows <#windows>`__

Common requirements
-------------------

- Use a host reserved for trusted jobs. Jobs run as the service account and
  can access its credentials and the host's persistent state.
- Install Git and the compilers, SDKs, and other tools your workflows need.
  The `first build example <./workflows.rst#first-build>`__ needs a C compiler;
  on Ubuntu/Debian, install ``git`` and ``build-essential``.
- Make tools available to the service account. Services do not inherit your
  interactive shell's environment, PATH modifications, or credentials.
- Allow outbound HTTPS to GitHub and the services your workflows use. hrc
  needs no inbound port to receive jobs. Ansible still needs its own management
  connection, such as SSH or WinRM.
- Provide disk space for workspaces, build output, shared caches, and job logs.
  Logs are retained independently of workspaces; plan their retention.

Release archives include the Worker's .NET 8 runtime and Node.js 20/24. A
separate .NET runtime, SDK, or Node.js installation is not required to run hrc.
Your workflows may need additional versions or SDKs for their own builds.

Linux
-----

Linux archives require glibc; Alpine/musl hosts are not supported. The supplied
service definitions support systemd and OpenRC. Container jobs, service
containers, and container actions require Docker and permission for the service
account to use it.

Install the native Worker dependencies below before deploying hrc. Install
Git and your build tools separately.

Ubuntu / Debian
~~~~~~~~~~~~~~~

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

RHEL / AlmaLinux / Rocky Linux / CentOS Stream / Fedora
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: sh

   sudo dnf install -y ca-certificates curl tar gzip glibc libgcc libstdc++ \
       krb5-libs tzdata zlib openssl-libs libicu lttng-ust

openSUSE Leap 16
~~~~~~~~~~~~~~~~

.. code-block:: sh

   sudo zypper -n install ca-certificates curl tar gzip glibc libgcc_s1 \
       libstdc++6 krb5 timezone zlib libopenssl3 libicu lttng-ust

macOS
-----

macOS 13.5 or later needs no additional native Worker dependencies. Install Git
and any build tools needed by your workflows, such as Xcode or Command Line
Tools, separately. Ensure the service account can access them.

Keep controller state and job directory paths free of spaces. For Ansible,
install Python on the host and arrange SSH and administrative access.

Windows
-------

Use PowerShell as Administrator for manual installation. Current Windows
versions include ``tar``. Ansible supports SSH/PowerShell or WinRM and needs
``ansible.windows`` 3+ on the Ansible controller.

Install Git and your toolchains for the service account, not just your login
session. The supplied service runs as ``LocalService``; account-specific
credentials and settings from your desktop session will not automatically be
available to jobs.

Antivirus scanning can affect large source trees and build output. If builds
are slow, measure the impact before changing protection settings. Where your
security policy allows it, consider narrowly scoped exclusions for dedicated
workspace and cache directories rather than disabling protection globally.
Exclusions reduce protection, especially on a host executing build code.

Next: `set up GitHub access <./github-auth.rst>`__.
