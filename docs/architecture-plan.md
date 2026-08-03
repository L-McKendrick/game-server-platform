

# **Project Overview & Architecture Plan**

## **1\. Project Summary**

**Objective:** Build an on-demand, self-managing game server platform that automatically provisions, operates, suspends, archives, and destroys dedicated game server infrastructure based on player activity.

**Primary Use Case:** Small cooperative groups running short campaigns (typically weekends or several consecutive evenings) separated by long periods of inactivity.

**Core Value Proposition:** Eliminate idle infrastructure costs by treating game servers as ephemeral workloads. Compute resources exist only while actively in use or during a short inactivity buffer, allowing infrastructure costs to return to effectively $0.00 during extended downtime.

**Design Principles:**

* Infrastructure is disposable.  
* Campaign data is persistent.  
* Automation is preferred over manual administration.  
* Recovery should resume from the last successful step whenever possible.  
* All infrastructure should be reproducible from stored configuration and campaign data.

---

## **2\. High-Level Architecture**

The platform is divided into four logical layers:

* Control Plane  
* Metadata Layer  
* Storage Layer  
* Compute Plane

---

## **3\. Control Plane**

The Control Plane is responsible for orchestrating the entire lifecycle of a campaign. It contains no long-running servers and is implemented using serverless or event-driven components.

### **Discord Interface**

Serves as the primary user interface.

Responsibilities include:

* Creating new campaigns  
* Uploading launcher presets  
* Uploading mission files  
* Selecting optional features  
* Starting campaigns  
* Waking sleeping servers  
* Manually stopping servers  
* Viewing campaign status  
* Restoring archived campaigns

---

### **Command/API Handler**

Receives requests from Discord and performs:

* Request validation  
* Permission checks  
* File validation  
* Campaign creation  
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

### **Campaign Manager**

Responsible for campaign lifecycle rather than infrastructure.

Responsibilities include:

* Tracking campaign state  
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

* Campaign identifier  
* Campaign owner  
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

Long-term durable storage for campaign assets.

Stores:

* Launcher presets  
* Mission files  
* Configuration templates  
* Campaign backups  
* Save archives  
* Server logs

Object storage remains even after infrastructure is removed.

---

### **Ephemeral Block Storage**

A high-performance SSD volume attached to the virtual machine during an active campaign.

Stores:

* Arma 3 server files  
* Creator DLC  
* Steam Workshop mods  
* TeamSpeak server  
* Active campaign saves  
* Runtime configuration

The volume exists only while the campaign is active or sleeping.

After archival, it is destroyed.

---

## **6\. Compute Plane**

### **Virtual Machine**

An on-demand virtual machine optimized for strong single-thread CPU performance.

Responsibilities include:

* Running Arma 3 Dedicated Server  
* Running TeamSpeak 3  
* Hosting campaign data  
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

Deployment may be optional depending on campaign configuration.

---

## **7\. Campaign State Machine**

Each campaign progresses through a defined lifecycle.

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
14. Mark campaign Ready

Each stage records completion so interrupted deployments can resume instead of restarting.

---

## **9\. Operational Lifecycle**

### **Campaign Deployment**

1. User creates a campaign through Discord.  
2. Launcher preset, mission, and optional assets are uploaded.  
3. Campaign Manager records metadata.  
4. Provisioning Service creates infrastructure.  
5. Bootstrap workflow installs required software.  
6. Health checks verify successful startup.  
7. Discord receives connection details and campaign status.

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
6. Campaign state changes to SLEEPING.

---

### **Automatic Archival**

After a configurable inactivity period:

1. Warning notifications are sent.  
2. Campaign saves are compressed.  
3. Logs are archived.  
4. Backup archive is uploaded to object storage.  
5. Metadata database records archive location.  
6. Virtual machine is deleted.  
7. SSD volume is deleted.  
8. Campaign enters ARCHIVED state.

Infrastructure costs return to storage-only costs.

---

### **Campaign Restoration**

An archived campaign can be restored at any time.

The platform:

1. Creates new infrastructure.  
2. Restores campaign archive.  
3. Reinstalls required software.  
4. Downloads missing Workshop content.  
5. Restores saves.  
6. Starts services.  
7. Returns the campaign to RUNNING state.

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

* CampaignCreated  
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
* CampaignRestored

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

The platform intentionally separates infrastructure from campaign data.

Virtual machines are considered disposable resources that can be recreated at any time.

Campaigns remain persistent through stored metadata and archived data, allowing complete infrastructure destruction without losing progress.

This architecture minimizes operational cost, reduces manual administration, improves fault recovery, and provides a foundation for supporting additional dedicated game servers beyond Arma 3 while reusing the same orchestration platform.

Based on your current architecture document, I'd build this in **vertical slices**, where each milestone results in a working system rather than building all of one layer first. The architecture already defines the major components and lifecycle, so the implementation order should follow the lifecycle from "create campaign" to "destroy campaign."

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

## **Phase 2 — Campaign Metadata**

**Goal:** Create and manage campaigns before worrying about infrastructure.

Implement:

* Campaign model  
* State machine  
* Database access layer  
* Campaign CRUD operations  
* File upload handling  
* Object storage integration

Deliverable:

Users can create campaigns, upload mission files and launcher presets, and view campaign status.

---

## **Phase 3 — Discord Bot**

**Goal:** Make Discord the primary interface early.

Implement commands such as:

* `/campaign create`  
* `/campaign list`  
* `/campaign status`  
* `/campaign delete`  
* `/campaign upload`  
* `/campaign start`

At this stage, `/campaign start` can simply change the campaign state without provisioning infrastructure.

Deliverable:

A functional Discord interface backed by the metadata layer.

---

## **Phase 4 — Infrastructure Provisioning**

**Goal:** Automatically create and destroy cloud resources.

Implement:

* VM creation  
* SSD creation  
* Volume attachment  
* Firewall creation  
* Public IP allocation  
* VM deletion  
* Volume deletion

Initially, stop after verifying that the VM is running.

Deliverable:

Discord can create and destroy cloud infrastructure.

---

## **Phase 5 — Bootstrap System**

This is likely the largest implementation milestone.

Break it into independent stages, as described in your bootstrap workflow.

Implement:

1. Retrieve secrets  
2. Install SteamCMD  
3. Install Arma 3  
4. Install Creator DLC  
5. Download Workshop mods  
6. Normalize Linux paths  
7. Deploy configuration  
8. Deploy mission  
9. Install TeamSpeak  
10. Launch services  
11. Health checks

Record completion after each stage to support resuming failed deployments.

Deliverable:

A newly provisioned VM automatically becomes a playable server.

---

## **Phase 6 — Monitoring**

Once the server works, begin monitoring it.

Implement:

* Arma query monitoring  
* Player count  
* TeamSpeak status  
* Process monitoring  
* Health checks  
* Automatic service restart

Deliverable:

The platform can detect failures and recover individual services.

---

## **Phase 7 — Sleep/Wake Automation**

Implement:

* Idle detection  
* Sleep timer  
* Graceful shutdown  
* VM stop  
* Public IP release  
* Wake command  
* Fast boot

Deliverable:

Campaigns can pause without being destroyed, minimizing compute costs while retaining the attached storage volume.

---

## **Phase 8 — Archival**

Now implement the long-term lifecycle.

Implement:

* Warning scheduler  
* Save compression  
* Log collection  
* Archive creation  
* Upload to object storage  
* Metadata updates  
* VM deletion  
* Volume deletion

Deliverable:

Inactive campaigns automatically transition to the archived state after the configured inactivity period.

---

## **Phase 9 — Restoration**

Implement:

* Restore command  
* Archive download  
* VM recreation  
* Volume recreation  
* Save restoration  
* Service startup

Deliverable:

Archived campaigns become fully restorable.

---

## **Phase 10 — Reliability**

Improve robustness rather than adding features.

Focus on:

* Retry logic  
* Idempotent provisioning  
* Workflow resumption  
* Error recovery  
* Timeouts  
* Cleanup of partially created resources

This phase turns a working prototype into a dependable platform.

---

## **Phase 11 — Observability**

Enhance visibility into system behavior.

Implement:

* Structured logging  
* Event history  
* Deployment progress  
* Metrics  
* Audit trail  
* Cost tracking  
* Dashboard

Deliverable:

Administrators can diagnose issues and monitor operations without accessing the VM directly.

---

## **Phase 12 — Polish & Future Features**

Only after the core lifecycle is stable should you add enhancements such as:

* Scheduled boots  
* Multiple concurrent campaigns  
* Automatic Workshop updates  
* Additional supported games  
* Web dashboard  
* Cost estimation  
* Backup versioning

## **Overall Roadmap**

1\. Foundation  
        │  
2\. Metadata Layer  
        │  
3\. Discord Bot  
        │  
4\. Cloud Provisioning  
        │  
5\. Server Bootstrap  
        │  
6\. Health Monitoring  
        │  
7\. Sleep / Wake  
        │  
8\. Archive & Destroy  
        │  
9\. Restore  
        │  
10\. Reliability  
        │  
11\. Observability  
        │  
12\. Feature Expansion

This sequence keeps the project continuously usable: each phase builds on the previous one and delivers a complete, testable capability instead of isolated components. It also aligns closely with the lifecycle defined in your architecture document, progressing from campaign creation through provisioning, operation, suspension, archival, and restoration.

# **Phase 1 Updated Technology Stack**

| Component | Decision |
| ----- | ----- |
| Cloud Provider | ✅ AWS |
| Compute | ✅ EC2 |
| Storage | ✅ EBS \+ S3 |
| Infrastructure as Code | ✅ Terraform *(recommended over CloudFormation)* |
| Language | ✅ Go |
| Discord | ✅ discordgo |
| Database | ✅ SQLite |
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

