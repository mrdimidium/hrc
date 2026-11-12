Manual
======

Follow these steps to bring a host from an unprepared machine to its first
GitHub Actions job. Each chapter covers one part of setup; use the same chapters
when adding another host or changing an existing installation.

1. **Prepare the host.** Check the `operating system requirements, dependencies,
   build tools, and service environment <./host-preparation.rst>`__.
2. **Set up GitHub access.** Choose repository- or organization-level
   registration and prepare a `GitHub App or PAT <./github-auth.rst>`__.
3. **Install hrc.** Use `Ansible (recommended) or release archives
   <./installation.rst>`__, verify the release, supply credentials and
   configuration, and start the service.
4. **Configure a workflow.** Connect ``runs-on`` to your configured runners and
   `run a first build <./workflows.rst>`__.

After setup, use `Operation <./operation.rst>`__ for service management,
logs, and cleanup. For lifecycle changes, follow `Upgrading <./upgrading.rst>`__
or `Removal <./removal.rst>`__.
The `Configuration reference <../configuration.rst>`__ describes every
configuration field and its defaults.

Jobs execute directly on the host. Use only trusted code; runner registration
and workspace cleanup do not provide isolation from the machine or its secrets.
