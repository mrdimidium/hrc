Workflow configuration
======================

After `installing and starting hrc <./installation.rst>`__, direct a workflow
to the scale set configured on your host. The examples below assume its name
is ``hrc-builds`` and no explicit labels are configured.

First build
-----------

On a Linux host with a C compiler available to the service account, commit
this as ``.github/workflows/first-build.yml`` on your repository's default branch:

.. code-block:: yaml

   name: First build
   on: workflow_dispatch
   jobs:
     build:
       runs-on: hrc-builds
       steps:
         - run: |
             printf '#include <stdio.h>\nint main(void) { puts("Hello from hrc"); }\n' > hello.c
             cc hello.c -o hello
             ./hello

Open **Actions → First build → Run workflow** in GitHub. The job compiles and
runs the program on your host; its log should contain ``Hello from hrc``.
This sample creates its own source file. For a build of your repository,
add a checkout step and your project's build commands instead.

Selecting runners
-----------------

With no ``labels`` in hrc's group configuration, the scale set name is its
routing label:

.. code-block:: yaml

   runs-on: hrc-builds

With explicit labels, use those labels instead. For example, a group configured
with ``"labels": ["linux", "X64"]`` is selected by:

.. code-block:: yaml

   runs-on: [linux, X64]

Explicit labels replace the default name label. hrc does not automatically add
``self-hosted``, OS, or architecture labels. Choose labels that distinguish the
hosts and environments your workflow needs. For organization registration,
the GitHub runner group's repository access policy must also allow the job.

Choosing a slot policy
----------------------

``reuseDirs`` is a policy of the configured hrc group, not a workflow option.
To offer both clean and warm environments on one host, configure separate
groups with distinct names or labels and route jobs to the appropriate group.
For example, with groups named ``checks`` and ``builds`` and no explicit labels:

.. code-block:: yaml

   jobs:
     lint:
       runs-on: checks
       steps:
         - run: echo "Use the group configured with reuseDirs disabled"
     build:
       runs-on: builds
       steps:
         - run: echo "Use the group configured with reuseDirs enabled"

A reusable slot retains files and processes until controller shutdown; it does
not guarantee that a particular job gets the same slot every time. Make build
commands safe for either a cold or warm workspace. See
`group configuration <../configuration.rst#groups>`__ and
`workspace lifetime <./operation.rst#workspaces-and-cleanup>`__.

If the job does not start
-------------------------

- Check that the service is running and its logs show a scale set connection.
- Match ``runs-on`` to the configured name or explicit labels.
- For organization runners, check the runner group's repository access.
- Check credentials, organization approval, token expiry, and available host
  capacity. A busy host cannot accept another runner until a slot is released.

Use the `service commands and logs <./operation.rst>`__ for diagnosis.
Next: `operate and maintain the host <./operation.rst>`__.
