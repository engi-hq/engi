# Security Policy

## Supported versions

Engi is pre-1.0. Only the latest release receives security fixes. Older `0.x` releases are not patched;
upgrade to the latest release.

## Reporting a vulnerability

Please report security issues privately. Do not open a public issue, pull request or discussion for a
vulnerability.

1. Go to the repository's **Security** tab on GitHub.
2. Choose **Report a vulnerability** (private vulnerability reporting, a GitHub security advisory).
3. Describe the problem and how to reproduce it.

Include, where you can:

- The Engi version or commit, and how you run it (standalone or cluster; binary, Docker or Helm).
- The steps to reproduce, with a minimal workflow or request if possible.
- The impact you expect, and who could be affected.
- Any suggested fix or mitigation.

Do not include real credentials, encryption keys or personal data in the report. Test only against
your own instances, not against systems you do not own.

## What to expect

Engi is a volunteer project, so response times are best effort. We aim to acknowledge a report within
7 days, then keep you updated on triage and the fix. We will credit you in the advisory and the
changelog unless you ask us not to.

## Scope notes

Some features are guards, not security boundaries. Please read these before reporting:

- **Python Code node.** Its import and dunder-attribute checks guard against mistakes. They are not a
  sandbox, because Python cannot be sandboxed in-process. See [docs/15-plugins.md section 15.8](docs/15-plugins.md#158-python-code-node)
  and the [Code node page](docs/nodes/04-code.md). For untrusted workflow authors, run workers in a
  container with no network and a read-only filesystem, or disable the node with `nodes.code.python.enabled`.
- **JavaScript Code node.** It runs in a sandboxed runtime with no filesystem or network access. It is still
  a workflow feature: anyone who can edit workflows should be trusted accordingly.
- **Plugins.** Plugins run as separate processes with the server's OS user. Engi does not sandbox them beyond
  process isolation and environment filtering. Run untrusted plugins as HTTP services in their own containers.
  See [docs/15-plugins.md section 15.6](docs/15-plugins.md#156-process-management-stdio).
- **Metrics endpoint.** `/metrics` is served outside API authentication by design, so Prometheus can scrape it.
  Bind it to a private address (`ENGI_METRICS_LISTEN`) or restrict it at the network level. See
  [docs/11-deployment.md](docs/11-deployment.md).
