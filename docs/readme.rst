Hosted Runner Controller
========================

``hrc`` is a small controller for GitHub runners on your own Windows, Linux, and
macOS machines. It automates runner registration, startup, updates, and cleanup
for a small fleet of build hosts.

Each machine is an independent ``hrc`` host running a daemon with a declarative
config. By design, ``hrc`` does not form clusters, communicate with other hosts,
or persist controller state. As jobs arrive, the daemon starts ephemeral runners
with a shared, read-only runtime and separate workspaces, then cleans them up
when they finish. Jobs run directly on the host, while installed tools and shared caches
persist between builds.

A runner slot owns a workspace and a process group. With ``reuseDirs``, both can
be reused across jobs during one controller run. Graceful controller shutdown
expires all slots, including idle reusable ones: remaining processes are killed
and slot directories are deleted. On all three platforms, process cleanup uses
the inherited ``RUNNER_TRACKING_GROUP`` environment variable and is best effort.
Windows also retains a Job Object as a crash-cleanup safety net. On Linux and
macOS, cleanup cannot run if the controller is killed with ``SIGKILL`` or crashes.

Although ephemeral build environments offer many advantages, stateful build
hosts are useful for a personal CI setup: they keep toolchains ready and caches
local, and accommodate Windows and macOS alongside Linux, whereas ARC supports
only Linux. Managing runners on these hosts also makes concurrent builds easier:
each GitHub runner handles one job at a time, requiring multiple instances.

.. warning::

   Shared, stateful runners are suitable only for private use with trusted code,
   since builds share the host's environment and persistent state.

Installation
------------

See the `installation guide <./installation.rst>`__ for macOS, Linux, and Windows,
then `configure and manage the service <./usage.rst>`__.

Package signing
---------------

The `package signing public key <https://pkg.dimidiumlabs.io/packages.gpg>`__
has the primary-key fingerprint:

.. code-block:: text

   60BD 9813 FF8B 4DED C9B7 EB67 52D9 2514 660E 2301

License
-------

Source code is licensed under `BSD-3-Clause <../LICENSES/BSD-3-Clause.txt>`__. Documentation is
licensed under `CC-BY-4.0 <../LICENSES/CC-BY-4.0.txt>`__.

The bundled GitHub runner is `MIT <../LICENSES/MIT.txt>`__ licensed;
its license and .NET dependency notices are included in the runner payload.
