# Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org).

> **Status:** applying. Releases published so far, including v0.1.0, are **not signed**. This page will say so until the first signed release.

## What is signed

The Windows programs (`esl.exe`, `EnterSpaceLight.exe`) and the Windows installers, for x64 and Arm. They are built from this repository's source by [GitHub Actions](../.github/workflows/release.yml) on GitHub-hosted runners. Binaries built anywhere else are never signed.

The installer includes the official [WinFsp](https://github.com/winfsp/winfsp) installer, unmodified and as released by the WinFsp project. We don't sign it.

## Team roles

| Role | Who |
|---|---|
| Authors (committers) and reviewers | [@blaineam](https://github.com/blaineam) |
| Approvers (approve every signing request) | [@blaineam](https://github.com/blaineam) |

Changes proposed by anyone who isn't a committer are reviewed by a team member before they're merged. Every release needs manual approval before it's signed. Team members use multi-factor authentication for GitHub and SignPath.

## Privacy

See the [privacy policy](../PRIVACY.md).

## Reporting a problem

If you believe a signed file doesn't match this policy, please report it as described in [SECURITY.md](../SECURITY.md).
