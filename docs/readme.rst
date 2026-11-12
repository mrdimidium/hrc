Hosted Runner Controller
========================

``hrc`` takes care of running self-hosted GitHub Actions runners on your own
Linux, macOS, and Windows servers. Install one controller per host and describe
its GitHub connections and execution settings, instead of scripting runner
registration, configuring individual instances, and cleaning up after jobs.
Choose fresh workspaces or keep files and build processes warm between jobs.

- **Three operating systems.** Linux, macOS, and Windows are first-class
  platforms, with the same configuration and runner lifecycle model.
- **Managed lifecycle.** One configuration covers GitHub App or PAT
  authentication, runner registration, concurrent execution, and cleanup.
  Runners share a read-only runtime instead of needing separate installations.
- **Warm builds.** Reusable slots retain both workspace files and child
  processes, such as a Bazel server. Use ephemeral slots for jobs that need
  cleanup after each run; both policies can coexist on one host.
- **Host capacity.** Set a per-runner CPU and memory quota and a host reserve;
  hrc calculates how many runners fit. These settings control concurrency,
  not the resources an individual job can consume.
- **Independent hosts.** No hrc cluster, shared database, or host-to-host
  connections. Each controller connects outbound to GitHub; receiving jobs
  needs no inbound ports or webhook receiver. Hosts need not share a network
  or location.
- **Ansible-ready.** The bundled collection installs, configures, starts,
  upgrades, and removes hrc on all three operating systems.

.. warning::

   Jobs currently execute directly on the host, without isolation. Use hrc only
   for trusted code: jobs share the machine and can access the service account's
   credentials. An ephemeral runner registration does not make the host
   disposable or isolate one job from another.

Example
-------

These Ansible role variables describe a Linux host with reusable build slots:

.. code-block:: yaml

   hrc_config:
     id: build-01
     quota: { cpu: 2, memory: 4GiB }
     groups:
       - url: https://github.com/your-org/your-repo
         name: builds
         reuseDirs: true
         pat: { type: file, value: /etc/hrc/secrets/github-token }
   hrc_secrets:
     github-token: "{{ vault_github_token }}"

After setup, a job in that repository can select these runners by name:

.. code-block:: yaml

   jobs:
     build:
       runs-on: builds
       steps:
         - run: echo "Running in a reusable slot on my build host"

This is a configuration and workflow excerpt, not a complete installation.
The role writes ``hrc_secrets`` to files on the host; ``pat`` points to that same
token file. Supply your repository URL and credentials, and choose quotas for
your host.
With no explicit ``labels``, the scale set name is the routing label. Omit
``reuseDirs`` or set it to ``false`` to clean up slots after each runner.

On this page
------------

- `Manual <#manual>`__
- `Why not just actions/runner? <#why-not-just-actions-runner>`__
- `Runners and slots <#runners-and-slots>`__
- `Compatibility <#compatibility>`__
- `Package signing <#package-signing>`__
- `License <#license>`__

Manual
------

Follow the `Manual <./manual/index.rst>`__ from host preparation and GitHub access
through installation to your first build. Ansible is the recommended deployment
method; release archives are the most portable alternative. Each chapter is
also usable on its own.

For an existing host, see `Operation <./manual/operation.rst>`__ and the
`Configuration reference <./configuration.rst>`__.

Why not just actions/runner?
----------------------------

The standalone ``actions/runner`` executes one job at a time. Even setting up
one instance involves delivering the runner, obtaining a registration token,
configuring its directory, and arranging for it to run as a service. Concurrent
jobs require multiple instances. Ephemeral registration is supported, but you
still need automation to prepare, register, start, and clean up each instance,
including obtaining tokens through a GitHub App if that is how you authenticate.

hrc provides that operational layer. Its Ansible collection handles deployment
and service setup; the controller uses your configured credentials to manage
runner registration, capacity, slots, and cleanup as jobs arrive. Execution uses
a bundled Worker built from GitHub's runner source, not a new workflow engine.

GitHub's `Actions Runner Controller (ARC)
<https://github.com/actions/actions-runner-controller>`__ manages runners through
Kubernetes on Linux. hrc takes a host-based approach with Linux, macOS, and
Windows as equal targets. Each host is autonomous: deploying more hosts does
not require building a cluster of hrc controllers.

Runners and slots
-----------------

**A new runner does not have to mean a cold build environment.** Each job gets a
new ephemeral runner registration, while the slot determines the lifetime of
its workspace and child processes:

- **Ephemeral slots** remove their workspace and remaining processes when the
  runner finishes. Use them for lint checks, hermetic builds, and other jobs
  that do not need a warm workspace.
- **Reusable slots** keep files and processes between jobs in the same
  configured group. Large incremental builds can reuse their build output and
  a running build server instead of starting cold each time.

Set ``reuseDirs`` per group to use both policies on one controller. Installed
tools and host-level caches outside slot directories remain available with
either policy. Resource accounting limits busy slots; it is not a hard CPU or
memory limit on job processes, and idle reusable slots can retain processes.
Without a quota, hrc does not limit concurrency through resource accounting.

Reusable slots live within a controller run, not across a normal restart.
Stopping or restarting hrc cancels active jobs and removes all slots, including
idle reusable ones. See `workspaces and cleanup
<./manual/operation.rst#workspaces-and-cleanup>`__ for cleanup guarantees and crash behavior.

Compatibility
-------------

hrc targets GitHub.com on Linux, macOS, and Windows, with ``amd64`` and ``arm64``
builds. See `Host preparation <./manual/host-preparation.rst>`__
for host requirements. GitHub Enterprise Server may work, but has not been
tested and is not currently a verified compatibility target.

Package signing
---------------

The `package signing public key <https://pkg.dimidiumlabs.io/packages.gpg>`__
has the primary-key fingerprint:

.. code-block:: text

   60BD 9813 FF8B 4DED C9B7 EB67 52D9 2514 660E 2301

See `release verification <./manual/installation.rst#download>`__ for checksum,
signature, and build provenance checks. Verify the signing key's fingerprint
through an independent trusted source before trusting it.

License
-------

Source code is licensed under `BSD-3-Clause <../LICENSES/BSD-3-Clause.txt>`__.
Documentation is licensed under `CC-BY-4.0 <../LICENSES/CC-BY-4.0.txt>`__.

The bundled GitHub runner is `MIT <../LICENSES/MIT.txt>`__ licensed;
its license and .NET dependency notices are included in the runner payload.
