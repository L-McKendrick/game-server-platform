

# **Project Overview & Architecture Plan**

## **1\. Project Summary**

**Objective:** Build an on-demand, self-managing game server platform that automatically provisions, operates, suspends, archives, and destroys dedicated game server infrastructure based on player activity.

**Primary Use Case:** Small cooperative groups running short sessions (typically weekends or several consecutive evenings) separated by long periods of inactivity.

**Core Value Proposition:** Eliminate idle infrastructure costs by treating game servers as ephemeral workloads. Compute resources exist only while actively in use or during a short inactivity buffer, allowing infrastructure costs to return to effectively $0.00 during extended downtime.

**Design Principles:**

* Infrastructure is disposable.  
* Automation is preferred over manual administration.  
* Recovery should resume from the last successful step whenever possible.  
* All infrastructure should be reproducible from stored configuration and game sessions data.

---

## **2\. High-Level Architecture**

The platform is divided into four logical layers:

* Control Plane  
* Metadata Layer  
* Storage Layer  
* Compute Plane

---

## **3\. Control Plane**

The Control Plane is responsible for orchestrating the entire lifecycle of a game session. It contains no long-running servers and is implemented using serverless or event-driven components.

### **Discord Interface**

Serves as the primary user interface.

Responsibilities include:

* Creating new sessions/missions  
* Uploading launcher presets  
* Uploading mission files  
* Selecting optional features  
* Starting sessions  
* Waking sleeping servers  
* Manually stopping servers  
* Viewing session status  
* Restoring archived sessions

---

### **Command/API Handler**

Receives requests from Discord and performs:

* Request validation  
* Permission checks  
* File validation  
* Session creation  
* Workflow initiation

It does not directly provision infrastructure.

---

### **Provisioning Service**

Responsible for infrastructure lifecycle.

Responsibilities include:

* Creating virtual machines  
* Creating storage volumes  
* Attaching storage  
* Creating networking resources  
* Assigning public IP addresses  
* Bootstrapping new servers  
* Destroying infrastructure when complete

---

### **Game Session Manager**

Responsible for session lifecycle rather than infrastructure.

Responsibilities include:

* Tracking session state  
* Monitoring sleep timers  
* Scheduling warnings  
* Initiating archival  
* Coordinating restoration  
* Managing workflow progress

---

### **Event Scheduler**

Executes periodic background tasks.

Examples include:

* Sleep timer checks  
* Warning notifications  
* Archive expiration  
* Retry failed workflows  
* Health verification  
* Automatic cleanup

---

## **4\. Metadata Layer**

A lightweight database stores orchestration state.

Unlike object storage, this layer contains structured metadata rather than files.

Typical information includes:

* Session identifier  
* Session owner  
* Current lifecycle state  
* VM identifier  
* Storage volume identifier  
* Public IP  
* Steam Workshop collection  
* Mission information  
* Sleep start time  
* Warning timestamps  
* Archive location  
* Discord channel information  
* Workflow progress

This metadata persists even after infrastructure has been destroyed.

---

## **5\. Storage Layer**

### **Object Storage**

Long-term durable storage for Session assets.

Stores:

* Launcher presets  
* Mission files  
* Configuration templates  
* Session backups  **likely not needed for most cases
* Save archives  
* Server logs

Object storage remains even after infrastructure is removed.

---

### **Ephemeral Block Storage**

A high-performance SSD volume attached to the virtual machine during an active session.

Stores:

* Arma 3 server files  
* Creator DLC  
* Steam Workshop mods  
* TeamSpeak server  
* Active session saves  **arma does not have saves for most missions
* Runtime configuration

The volume exists only while the session is active or sleeping.

After archival, it is destroyed.

---

## **6\. Compute Plane**

### **Virtual Machine**

An on-demand virtual machine optimized for strong single-thread CPU performance.

Responsibilities include:

* Running Arma 3 Dedicated Server  
* Running TeamSpeak 3  
* Hosting session data  
* Executing mission scripts  
* Managing Workshop content

Using on-demand compute eliminates the risk of instance eviction during gameplay.

---

### **Application Services**

#### **Arma 3 Dedicated Server**

Hosts:

* Missions  
* Mods  
* Creator DLC  
* Save files  
* Player progression

---

#### **TeamSpeak 3 Server**

Runs alongside Arma 3 to support TFAR, ACRE2, or standard voice communication.

Deployment may be optional depending on mission configuration.

---

## **7\. Session State Machine**

Each session progresses through a defined lifecycle.

NEW

↓

PROVISIONING

↓

BOOTSTRAPPING

↓

INSTALLING

↓

READY

↓

RUNNING

↓

IDLE

↓

SLEEPING

↓

WARNING\_1

↓

WARNING\_2

↓

ARCHIVING

↓

DESTROYING

↓

ARCHIVED

↓

RESTORING (optional)

↓

RUNNING

Explicit states simplify automation, recovery, monitoring, and future expansion.

---

## **8\. Bootstrap Workflow**

Provisioning is divided into resumable stages.

1. Create VM  
2. Create and attach SSD volume  
3. Configure networking  
4. Retrieve secrets  
5. Install or verify Arma 3  
6. Install Creator DLC  
7. Download Workshop content  
8. Normalize Linux file paths  
9. Deploy configuration  
10. Deploy mission  
11. Start TeamSpeak  
12. Start Arma 3  
13. Perform health checks  
14. Mark session Ready

Each stage records completion so interrupted deployments can resume instead of restarting.

---

## **9\. Operational Lifecycle**

### **Session Deployment**

1. User creates a session through Discord.  
2. Launcher preset, mission, and optional assets are uploaded.  
3. Session Manager records metadata.  
4. Provisioning Service creates infrastructure.  
5. Bootstrap workflow installs required software.  
6. Health checks verify successful startup.  
7. Discord receives connection details and session status.

---

### **Active Operation**

During gameplay the platform continuously monitors:

* VM availability  
* Arma server process  
* TeamSpeak process  
* Server query responsiveness  
* Mission status  
* Player count

If an individual service fails, only that service is restarted where possible.

---

### **Automatic Sleep**

When no players are detected for a configurable period, or when manually requested:

1. Game server shuts down.  
2. Operating system performs a clean shutdown.  
3. Virtual machine enters a stopped state.  
4. Public IP is released.  
5. SSD volume remains attached for rapid restoration.  
6. Session state changes to SLEEPING.

---

### **Automatic Archival**

After a configurable inactivity period:

1. Warning notifications are sent.  
2. Session saves are compressed.  (if game requires save data)
3. Logs are archived.  
4. Backup archive is uploaded to object storage.  
5. Metadata database records archive location.  
6. Virtual machine is deleted.  
7. SSD volume is deleted.  
8. Session enters ARCHIVED state.

Infrastructure costs return to storage-only costs.

---

### **Session Restoration**

An archived session can be restored at any time.

The platform:

1. Creates new infrastructure.  
2. Restores session archive.  
3. Reinstalls required software.  
4. Downloads missing Workshop content.  
5. Restores saves.  
6. Starts services.  
7. Returns the session to RUNNING state.

---

## **10\. Monitoring & Health Management**

The platform monitors multiple health indicators rather than relying solely on player count.

Examples include:

* VM reachability  
* Operating system health  
* Disk usage  
* Available memory  
* Arma server status  
* TeamSpeak status  
* Server query response  
* Active mission  
* Connected players

Health events generate structured logs and optional Discord notifications.

---

## **11\. Logging & Event Tracking**

Every significant operation generates a structured event.

Examples include:

* SessionCreated  
* ProvisionStarted  
* VMCreated  
* StorageAttached  
* SteamAuthenticated  
* WorkshopDownloadStarted  
* WorkshopDownloadCompleted  
* MissionDeployed  
* TeamSpeakStarted  
* ArmaStarted  
* HealthCheckPassed  
* SleepStarted  
* WarningIssued  
* ArchiveCreated  
* ArchiveUploaded  
* InfrastructureDestroyed  
* SessionRestored

Structured logging simplifies troubleshooting, auditing, and future analytics.

---

## **12\. Security**

The platform follows a least-privilege security model.

Key practices include:

* Steam credentials stored in managed secret storage  
* Minimal cloud permissions for virtual machines  
* Temporary authentication wherever possible  
* No embedded credentials in deployment scripts  
* Restricted firewall rules  
* Encrypted object storage  
* Secure archive transfers  
* Comprehensive audit logging

---

## **13\. Design Strategy**

The platform intentionally separates infrastructure from session data.

Virtual machines are considered disposable resources that can be recreated at any time.

Sessions remain persistent through stored metadata and archived data, allowing complete infrastructure destruction without losing progress.

This architecture minimizes operational cost, reduces manual administration, improves fault recovery, and provides a foundation for supporting additional dedicated game servers beyond Arma 3 while reusing the same orchestration platform.

Based on your current architecture document, I'd build this in **vertical slices**, where each milestone results in a working system rather than building all of one layer first. The architecture already defines the major components and lifecycle, so the implementation order should follow the lifecycle from "create session" to "destroy session."

## **Phase 1 — Foundation**

**Goal:** Establish the project skeleton and cloud infrastructure.

Build:

* Project structure  
* Configuration management  
* Logging framework  
* Metadata database schema  
* Object storage bucket  
* Secret management  
* Basic cloud authentication  
* CI/CD pipeline  
* Infrastructure-as-Code (Terraform/Pulumi/etc.)

---

## **Phase 2 — Metadata Layer**

**Goal:** Create and manage sessions before worrying about infrastructure.

Implement:

* Game model  
* State machine  
* Database access layer  
* CRUD operations  
* File upload handling  
* Object storage integration


---

## **Phase 3 — Discord Bot**

**Goal:** Make Discord the primary interface early.

Implement commands such as:

* `/create`  
* `/list`  
* `/status`  
* `/delete`  
* `/upload`  
* `/start`

At this stage, `/ start` can simply change the game state without provisioning infrastructure.

Deliverable:

A functional Discord interface backed by the metadata layer.

---

## **Phase 4 — Workflow Foundation**

**Status:** Complete.

Delivered:

* FIFO command, artifact, and notification queues with dead-letter queues
* Normalized command envelopes
* DynamoDB workflow records and conditional per-session leases
* Step Functions Standard workflow boundaries
* Artifact and notification workers
* Discord-native guild role configuration

The Phase 4 lifecycle state machines fail closed until their implementation phase. This prevents Discord from claiming that compute exists before provisioning is operational.

---

## **Phase 5 — Infrastructure Provisioning**

**Goal:** Create cost-bounded, discoverable AWS infrastructure without claiming that Arma is playable.

Implement:

* A dedicated VPC with two public subnets and no NAT Gateway
* Restrictive game and optional voice security groups with no inbound SSH
* EC2 instance role and Systems Manager access
* Encrypted EC2 root and persistent EBS data volumes
* Idempotent instance discovery and launch using session tags and client tokens
* DynamoDB-backed global capacity slots and approved compute profiles
* AWS Budget notifications before provisioning is enabled
* A command worker and staged `ProvisionSession` workflow
* Conditional metadata updates for resource identifiers and workflow progress

The workflow stops at `BOOTSTRAPPING`. `/session start` remains feature-gated until a reviewed deployment and end-to-end provisioning test succeed.

Deliverable:

Discord can request infrastructure provisioning; one tagged EC2 instance and its encrypted EBS volumes become reachable through Systems Manager without exposing SSH.

---

## **Phase 6 — Arma Bootstrap**

Implement resumable stages for secrets, SteamCMD, Arma 3, Creator DLC, Workshop content, Linux path normalization, configuration, mission deployment, optional TeamSpeak, service launch, and health checks.

Deliverable: a newly provisioned instance becomes a playable server.

---

## **Phase 7 — Monitoring**

Implement agent heartbeat, Arma query/player count, service and TeamSpeak checks, metrics, alarms, and selective recovery.

---

## **Phase 8 — Sleep and Wake**

Implement idle detection, graceful stop, EC2 stop/start, endpoint refresh, and fast wake while retaining the EBS data volume.

---

## **Phase 9 — Archive and Restore**

Implement warnings, versioned manifests, compression, checksums, S3 verification, compute and volume destruction, infrastructure recreation, and restore.

---

## **Phase 10 — Reliability**

Implement bounded retries, DLQ operations, reconciliation, orphan cleanup, cancellation, and disaster recovery.

---

## **Phase 11 — Production Hardening**

Complete least-privilege review, GitHub OIDC deployment, staging, threat-model review, runbook testing, dashboards, and cost verification.

---

## **Phase 12 — Expansion**

Only after the lifecycle is stable: additional games, scheduling and cost analytics, a web dashboard, multi-account, and multi-region support.

## **Overall Roadmap**

1. Foundation
2. Metadata layer
3. Discord interface
4. Workflow foundation
5. Infrastructure provisioning
6. Arma bootstrap
7. Monitoring
8. Sleep and wake
9. Archive and restore
10. Reliability
11. Production hardening
12. Expansion

This sequence keeps the project continuously usable: each phase builds on the previous one and delivers a complete, testable capability instead of isolated components. It also aligns closely with the lifecycle defined in your architecture document, progressing from session creation through provisioning, operation, suspension, archival, and restoration.

# **Phase 1 Updated Technology Stack**

| Component | Decision |
| ----- | ----- |
| Cloud Provider | ✅ AWS |
| Compute | ✅ EC2 |
| Storage | ✅ EBS \+ S3 |
| Infrastructure as Code | ✅ Terraform *(recommended over CloudFormation)* |
| Language | ✅ Go |
| Discord | ✅ Signed HTTP interactions using the Go standard library (ADR 0004) |
| Database | ✅ DynamoDB (deployed authority; in-memory repositories for local tests) |
| Secrets | ✅ AWS Secrets Manager |
| Scheduler | ✅ EventBridge |
| Serverless | ✅ AWS Lambda |
| Logging | ✅ Structured JSON (schema TBD) |
| Bootstrap | ✅ Bash |
| Source Control | ✅ GitHub |
| CI/CD | ✅ GitHub Actions |

#  Phase 2

| Recommended |
| ----- |
| **Metadata Layer** |
| **Server** or **Deployment** (generic) |
| **Mission Save** (Arma-specific) |
| **Mission Archive** or **Server Archive** (depending on what's included) |
| **Deployment State** |
| **Deployment Manager** or **Lifecycle Manager** |

For Arma-specific objects, use:

* Mission  
* Mission Save  
* Mission File (`.pbo`)  
* Mission Parameters  
* Mission Archive (if archiving only mission-related data)

For platform-wide concepts, use:

* Server  
* Deployment  
* Metadata  
* Infrastructure  
* Archive  
* Workflow  
* Lifecycle

This gives you a clean separation:

* **Platform terminology** stays generic, so adding another game later doesn't require renaming major components.  
* **Game terminology** remains familiar to users. An Arma admin still uploads a **Mission** and receives a **Mission Save**, while a future Minecraft server would upload a **World** instead.
