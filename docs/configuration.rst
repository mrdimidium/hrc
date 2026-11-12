Configuration
=============

hrc reads a JSON file supplied with ``hrc -config PATH``. Configuration is read
at startup; restart the service to apply changes. For installation examples,
see `Installation <./manual/installation.rst>`__. Restarting cancels active jobs and
removes all slots.

Contents
--------

- `Host settings <#host-settings>`__
- `Groups <#groups>`__
- `Resources <#resources>`__
- `Secrets <#secrets>`__
- `Ansible variables <#ansible-variables>`__
- `Command line <#command-line>`__

Host settings
-------------

.. list-table::
   :header-rows: 1

   * - Field
     - Default
     - Meaning
   * - ``id``
     - Required
     - Host identifier used in GitHub sessions and runner names. Give each host a distinct ID.
   * - ``groups``
     - No groups
     - Array of GitHub connections and slot policies. Configure at least one to run jobs.
   * - ``quota``
     - CPU and memory both zero
     - Resource accounting per busy runner. Without a quota, concurrency is not limited by resource accounting.
   * - ``reserve``
     - CPU and memory both zero
     - Capacity excluded from runner scheduling for the host itself.
   * - ``pathWorker``
     - ``./hrc-worker``
     - Worker executable. A bare filename is resolved through PATH.
   * - ``dirRunner``
     - ``./runner``
     - Shared read-only Worker resources.
   * - ``dirBuilds``
     - ``./hrc/build``
     - Parent of slot workspaces.
   * - ``dirLogs``
     - ``./hrc/log``
     - Parent of per-job diagnostics under ``jobs/``.

Relative paths use the controller's working directory, not the directory
containing the JSON file. The supplied services use their state directory as
working directory. Keep state and job directory paths free of spaces on macOS.
The Ansible role supplies installation-specific defaults for all four paths;
the table above describes the controller's own defaults.

Groups
------

A configured group describes one GitHub runner scale set and its slot policy.
Several configured groups can share a host's capacity. Do not confuse these
entries with GitHub's runner groups.

.. list-table::
   :header-rows: 1

   * - Field
     - Default
     - Meaning
   * - ``url``
     - Required
     - Full repository or organization URL, for example ``https://github.com/OWNER/REPO``.
   * - ``name``
     - Required
     - Scale set name, unique within this host's configuration. Also used to name reusable slot directories.
   * - ``group``
     - ``default``
     - GitHub runner group containing the scale set. Organization policies determine which repositories can use it.
   * - ``labels``
     - Scale set name
     - With no explicit labels, jobs select the scale set by ``name``. Otherwise only the configured labels are used.
   * - ``pat``
     - Not set
     - PAT described by a secret object.
   * - ``app``
     - Not set
     - GitHub App credentials: ``clientId`` (string), ``installationId`` (positive integer), and ``privateKey`` (secret object).
   * - ``reuseDirs``
     - ``false``
     - Retain the slot's workspace and child processes between jobs in this group, until controller shutdown.

Supply either ``pat`` or ``app``. If both mappings validate, hrc selects the App.
See `GitHub access <./manual/github-auth.rst>`__ for credential setup and permissions.
With ``reuseDirs``, ``name`` must also be a valid directory name: it cannot be
``.`` or ``..``, or contain a slash, backslash, or colon.

Changing labels recreates the scale set on startup, rather than updating it in
place. Other hosts using the same scale set should agree on its labels.
There are no automatic ``self-hosted``, OS, or architecture labels; include
those explicitly if you want to route by them. See
`Workflow configuration <./manual/workflows.rst>`__ for examples.

Resources
---------

``quota`` and ``reserve`` each accept ``cpu`` and ``memory``:

.. code-block:: json

   "quota": {"cpu": 2, "memory": "4GiB"},
   "reserve": {"cpu": 1, "memory": "2GiB"}

- ``cpu`` is a non-negative number of logical CPU threads, including fractions.
- ``memory`` is a JSON string containing integer bytes or an integer followed
  by ``KiB``, ``MiB``, ``GiB``, or ``TiB``, for example ``"4GiB"``.
- Zero or an omitted quota field leaves that resource unaccounted.

For each accounted resource, hrc subtracts the reserve from host capacity and
divides the remainder by the per-runner quota, rounding down. The smallest
result is the shared concurrency limit for all groups on the host. Startup
fails if the configured resources leave no room for a runner.

These are scheduling values, **not enforced CPU or memory limits**. Capacity
is calculated at startup, not continuously adjusted to currently free memory.
Idle reusable slots can retain processes even though they do not occupy a busy
runner slot. See `Workspaces and cleanup
<./manual/operation.rst#workspaces-and-cleanup>`__ for slot lifetime.

Secrets
-------

``pat`` and ``app.privateKey`` use the same object format:

.. code-block:: json

   {"type": "file", "value": "/etc/hrc/secrets/github-token"}

.. list-table::
   :header-rows: 1

   * - ``type``
     - Meaning of ``value``
   * - ``file``
     - Path to a file readable by the service account.
   * - ``env``
     - Name of an environment variable in the controller's environment.
   * - ``inline``
     - The secret itself. Anyone who can read the JSON configuration can read it.

Leading and trailing whitespace is removed from secret values; an empty secret
is an error. Services do not inherit your interactive shell's ``GITHUB_TOKEN``.
Jobs execute as the service account and can access its credentials; secret files
are not an isolation boundary between hrc and build code.

Ansible variables
-----------------

.. list-table::
   :header-rows: 1

   * - Variable
     - Default
     - Meaning
   * - ``hrc_config``
     - Required for installation
     - Mapping written as JSON. Must contain ``id`` and a nonempty ``groups`` array.
   * - ``hrc_secrets``
     - ``{}``
     - Simple filenames mapped to secret contents, written under the target's secrets directory.
   * - ``hrc_artifact_source``
     - URL embedded by release CI
     - Base HTTP(S) URL or absolute directory on the Ansible controller containing release archives.
   * - ``hrc_remove_data``
     - ``false``
     - Remove role only: also delete configuration, secrets, state, and logs at default locations.

The install role supplies ``pathWorker``, ``dirRunner``, ``dirBuilds``, and
``dirLogs``; ``hrc_config`` can override them. Other fields pass through unchanged.
Secret filenames may contain letters, digits, underscores, dots, and hyphens,
and must start with a letter, digit, or underscore. Omitted secret files are
retained; remove or revoke credentials explicitly.

Each collection pins a binary release and its checksums. To vendor binaries,
copy the release tarballs without renaming them and set ``hrc_artifact_source``
to their directory. Changing the source does not disable checksum verification.
There is no separate binary version override.

Command line
------------

- ``hrc -config PATH`` starts the controller using the specified JSON file.
- ``hrc -version`` prints the build version.
- ``hrc -help`` shows the available flags.
