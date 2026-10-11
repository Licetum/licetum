# Security policy

## Supported versions

Security fixes go to the `main` branch and to the most recent release; older releases are not patched.

## Reporting a vulnerability

Please do not report vulnerabilities in public issues, pull requests or discussions.

Use GitHub's private vulnerability reporting instead:
**[Report a vulnerability](https://github.com/Licetum/licetum/security/advisories/new)**.

Include:

- the component (`core/`, `admin-api/`, `web/`, the Docker images or the Compose setup) and the commit or version;
- the steps to reproduce, or a proof of concept, and what an attacker gains;
- whether the issue is already public anywhere.

The maintainers confirm the report, work on a fix in a private advisory, and publish the fix together with the advisory.
You are credited in the advisory unless you ask not to be.

## Scope

In scope: anything that lets a caller read or change access it should not have, bypass the checks of the check API or the
admin API, reach another tenant's data, or expose secrets; and vulnerabilities in the released images and in the
documented deployment.

Out of scope:

- the development credentials in the Compose stack (`admin@example.com` / `password`, the local Dex client). They are public
  by design and must never be used in a real deployment;
- findings that need a platform administrator to act against their own instance;
- reports produced by automated scanners without a demonstrated impact.

## Handling secrets

Do not commit passwords, tokens, private keys, production connection strings or real user data, including in test
fixtures. If you find a committed secret, report it through the same channel.
