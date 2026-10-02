---
hide:
  - navigation
  - toc
---

<div class="vs-hero" markdown>

![VaanarSena emblem](assets/brand/lockup-light.png#only-light){ .vs-hero-mark }
![VaanarSena emblem](assets/brand/lockup-dark.png#only-dark){ .vs-hero-mark }

# VaanarSena

**Open source, self-hosted device management** for iOS, iPadOS, macOS,
Windows, Android, ChromeOS and Linux. Company-owned and personal devices, one
console, one REST API, your own infrastructure.

[Get started](getting-started.md){ .md-button .md-button--primary }
[See the console](console.md){ .md-button }

</div>

![The VaanarSena overview](assets/screens/overview.png){ .vs-shot }

## Why VaanarSena

**Complete control.** One Go binary and one PostgreSQL database, with your own
certificate authority. No SaaS in the middle, no telemetry, no licence server.

**Native protocols.** Apple MDM, Windows MDM (MS-MDE2 and OMA-DM), the Android
Management API and the Google Admin SDK, plus a small agent for Linux. Each
platform is managed through its own channel.

**Personal devices stay personal.** Ownership is set by the enrollment link,
never claimed by the device. On personal devices the server refuses erase,
lock, location, scripts and hardware inventory; Retire removes only company
data.

**One policy, every platform.** Write a passcode, encryption, restriction,
Wi-Fi, update and app policy once. When that is not enough, ship raw Apple
payloads, a whole `.mobileconfig`, Windows OMA-URI settings or Android
Management API fields.

**Groups that keep up.** Smart groups fill themselves from rules such as
"iPhones below iOS 17" or "laptops without disk encryption", and
configuration follows membership. Blueprints onboard every device that falls
in scope, exactly once.

**Configuration as code.** Groups, policies and blueprints are YAML
manifests: apply them with `vsctl`, the API, or a Git directory the server
reconciles on its own.

**Auditable.** Every change, sign-in and refused command lands in an
append-only log that the database itself refuses to modify.

## Where to go next

- [Getting started](getting-started.md): run it locally in five minutes, or on a server with HTTPS.
- [Hosting](hosting.md): single VM with Caddy, Kubernetes with Helm, sizing, backups.
- [Platform setup](platforms.md): connect Apple, Android and ChromeOS; enroll Windows and Linux.
- [The console](console.md): a tour of the web console.
- [Manifests and GitOps](manifests.md): custom payloads, smart groups, blueprints, `vsctl`.
- [Security](security.md): the threat model and what the BYOD guard guarantees.
- [REST API](api.md): every endpoint.
