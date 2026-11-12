GitHub access
=============

Choose where runners register, then give hrc credentials for that scope.
The controller obtains and renews runner registration tokens itself; you do
not run ``config.sh`` or supply a short-lived registration token manually.

Registration scope
------------------

Set each configured group's ``url`` to one of:

- **Repository:** ``https://github.com/OWNER/REPO``.
- **Organization:** ``https://github.com/ORG``. Make sure the selected GitHub
  runner group permits access from the repositories that will use it.

A host can have several configured groups with different URLs or credentials.
The configuration's ``group`` field selects a GitHub runner group; ``name``
is the scale set name. See the `configuration reference
<../configuration.rst#groups>`__ for the distinction.

GitHub App
----------

A GitHub App is the recommended choice for ongoing use.

1. `Register a GitHub App
   <https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app>`__
   owned by the organization where it will be installed.
2. Grant the permissions for your registration scope:

   - **Repository:** Repository permissions → **Administration: Read and write**.
     Repository Metadata read access is granted automatically.
   - **Organization:** Organization permissions → **Self-hosted runners:
     Read and write**. Repository Administration write access is not required
     just to register organization runners.

3. Install the App on the target organization, including the target repository
   when registering at repository scope. Approve the requested permissions.
4. Copy its **Client ID** from the App settings and the **installation ID**
   from the installation settings URL. hrc uses ``clientId``, not the App's
   numeric App ID.
5. Generate and download a private key. Keep it secret and deploy it as a file
   readable by the hrc service account during `installation <./installation.rst>`__.

Use an ``app`` mapping in place of ``pat`` in the installation examples:

.. code-block:: json

   "app": {
     "clientId": "YOUR-APP-CLIENT-ID",
     "installationId": 123456,
     "privateKey": {"type": "file", "value": "/etc/hrc/secrets/github-app.pem"}
   }

Adjust the key path for your operating system. With Ansible, add an entry named
``github-app.pem`` to ``hrc_secrets`` with the PEM content from a secret variable,
so the role creates that file. Do not commit the key to a playbook or repository.

Personal access token
---------------------

A PAT is a shorter setup path and is used in the installation example.
The token's owner must be permitted to administer runners at the chosen scope.

For a `fine-grained PAT
<https://github.com/settings/personal-access-tokens/new>`__:

1. Set **Resource owner** to the repository or organization's owner.
2. For repository registration, use **Only select repositories** and select
   the target repository.
3. Grant **Repository permissions → Administration: Read and write** for
   repository registration, or **Organization permissions → Self-hosted
   runners: Read and write** for organization registration.
4. Obtain organization approval if required. A pending or expired token cannot
   register runners.

For a PAT classic, GitHub documents ``repo`` for repository registration and
``admin:org`` for organization registration. Its organization registration
endpoint also notes ``repo`` for private-repository access. Authorize classic
PATs for SAML SSO where the organization requires it.

Deploy the PAT as a secret file during installation. These credentials belong
to hrc; they are separate from the workflow's ``GITHUB_TOKEN``.

Permission sources
------------------

The scale set client exchanges your credentials for a runner registration
token, then an Actions Service token for scale set operations. There is no
separate scale set permission to grant.

The repository endpoint requires `Administration write
<https://docs.github.com/en/rest/actions/self-hosted-runners#create-a-registration-token-for-a-repository>`__;
the organization endpoint requires `Self-hosted runners write
<https://docs.github.com/en/rest/actions/self-hosted-runners#create-a-registration-token-for-an-organization>`__.
These endpoints accept GitHub App installation tokens and fine-grained PATs.
GitHub's `ARC authentication guide
<https://docs.github.com/en/actions/tutorials/use-actions-runner-controller/authenticate-to-the-api>`__
also lists Administration read for organization fine-grained PATs; the
registration endpoint used by hrc lists only Self-hosted runners write.

Next: `install and configure hrc <./installation.rst>`__.
