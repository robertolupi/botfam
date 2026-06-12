# The Architecture of Agentic Harnesses: Security, Isolation, and Collaborative Paradigms in Autonomous Coding Workflows

## Decoupling the Agent from the Host: The Market for Local Multi-Agent Harnesses

As artificial intelligence models have evolved from passive, prompt-driven chat interfaces into autonomous software engineering agents, the engineering bottleneck has shifted from raw model capability to systemic runtime architecture. Standard large language models routinely produce capable code in isolation; however, when deployed on multi-step, open-ended development tasks, their performance frequently degrades due to context window saturation, recursive error loops, and a lack of systemic validation. To mitigate these failures, software engineers have moved beyond basic prompt engineering toward what is now formalized as harness engineering. A harness represents the structural control layer of a multi-agent system, defining when specific agents run, how information flows between them, how their execution states are preserved, and how their outputs are verified.

Historically, development teams building multi-agent applications relied on ad-hoc orchestration—starting with simple iterative loops that gradually grew into unmaintainable patches of custom glue code. This approach inevitably fails under production pressures because it couples the agent topology to the application logic, making the system fragile, difficult to debug, and highly susceptible to model updates. Modern harness design enforces a strict separation of concerns, utilizing uniform interfaces to orchestrate complex, multi-agent interactions while ensuring deterministic execution, safety, and persistent memory.

A central question in this domain is whether lightweight, local-first multi-agent harnesses exist to manage these challenges without cloud dependencies. The market confirms the existence of several such systems, though they split along distinct structural and linguistic lines. The framework known as Hydra represents a security-first, containerized sandbox built primarily on Node.js and TypeScript.

Conversely, developers seeking a pure Python-based multi-agent harness frequently adopt AIPass, a highly structured, local-first CLI framework hosted on PyPI that leverages the Trinity Pattern for persistent agent identity and communication. This bifurcation represents a broader division in the competitive landscape, where security-oriented isolation, local git-worktree partitioning, shared collaborative workspaces, and behavioral skills frameworks represent competing architectural philosophies.

## Taxonomy of Modern Agentic Harnesses

The market for local-first multi-agent harnesses is highly competitive, defined by how frameworks resolve the tension between strict workspace isolation and fluid agent collaboration. The following table categorizes the leading solutions, their core technologies, and their mechanisms for managing memory and execution:

|**Framework**|**Primary Language & Runtime**|**Core Architectural Philosophy**|**Security & Isolation Mechanism**|**Memory & Context Management**|**Primary Target Workloads**|
|---|---|---|---|---|---|
|**Hydra**|TypeScript / Node.js|Zero-trust containerization|Isolated Docker containers with dual-file mount verification|Filesystem-based IPC via JSON polling|Processing untrusted third-party data and executing risky bash operations|
|**AIPass**|Python 3.10+|Shared-workspace collaboration|None (logical scheduling, dispatch locks, and PR write blocks)|Trinity Pattern: structured `id.json`, `local.json`, and `observations.json`|Long-horizon codebase maintenance and multi-agent collaborative development|
|**Emdash**|TypeScript / React / Electron|Agent-agnostic desktop command center|Git-worktree partitioning in a sibling directory structure|Local SQLite database and OS-level keychain credentials|Parallel multi-ticket resolution and cross-platform terminal management|
|**T3 Code**|TypeScript / Electron|Graphical "Bring Your Own Key" (BYOK) front-end|Automated local Git-worktree execution|Thread-based visual histories and project-level configurations|Iterative UI prototyping, codebase auditing, and automated PR publishing|
|**Obra Superpowers**|Composable Markdown Skills|Procedural behavioral intervention|None (delegated to host platform execution)|Granular, text-based micro-task plans and strict TDD verification loops|Enforcing strict test-driven development (TDD) and preventing context drift|

## Category 1: Security-First Containerized Sandboxes

The framework known as Hydra represents a security-first architecture designed to counter the critical vulnerabilities inherent in running autonomous coding agents directly on host machines. Typical developer-focused agentic frameworks prompt the host user for runtime permissions or execute shell commands directly within the host's terminal session. This design exposes developers to catastrophic supply-chain and execution risks, including prompt-injection attacks from fetched web pages that can compromise sensitive local assets, such as harvesting `~/.ssh/id_rsa` or AWS credentials.

Hydra addresses this structural vulnerability by enforcing complete containerized isolation. It is written as a local-first, Node.js-based CLI tool where every active agent operates entirely within an isolated Docker container. This structural constraint prevents agents from possessing default access to the host filesystem, environment variables, or active shell sessions.

### The Dual-File Validation Protocol

To permit agents to read and modify local files safely, Hydra utilizes a strict dual-file approval model that fails closed. Access to host paths requires mutual agreement between two independent configurations:

- **`hydra.yml`**: A project-level configuration file that declares the specific directories and assets the agent is requesting to mount.
    
- **`~/.config/hydra/mount-allowlist.json`**: A global, host-level configuration file stored in the user’s home directory that explicitly registers which directories are permitted to be mounted across any active agent sessions.
    

Both configurations must agree for a directory mount to succeed. If the global allowlist is absent, all additional mounts are blocked by default. Furthermore, Hydra prevents directory traversal attacks by resolving all symlinks before performing path validation. Highly sensitive system paths—such as `.ssh`, `.gnupg`, `.aws`, and `.env` files—are hard-blocked at the architectural level and can never be mounted into an agent container, regardless of configuration.

### Direct and Orchestrated Communication Pathways

Hydra provides two primary modes of operation to balance developer interactivity with automated task execution:

- **Direct Interactive Sessions (`hydra exec <agent_name>`)**: This pipeline establishes a direct TTY pass-through into the agent's container, spawning a native shell (such as a Claude Code terminal session). Because this connection utilizes direct terminal stream redirection, it avoids intermediate message serialization or third-party conversation logging, preserving local privacy.
    
- **Orchestrated Sessions**: Designed for asynchronous and automated execution, this pattern leverages filesystem-based Inter-Process Communication (IPC). A parent host process monitors local agent directories, polling and validating JSON messages written to disk to route data back and forth to external channels, such as Telegram integrations.
    

## Category 2: Shared-Workspace Collaborative Scaffolds

In contrast to containerized sandboxes, the **AIPass** framework and its underlying **Trinity Pattern** advocate for a zero-sandbox, collaborative workspace model. The core philosophy of this Python-based framework posits that isolating agents into discrete, sandboxed directories prevents them from functioning as a cohesive team. If Agent A cannot see what Agent B is currently building in real-time, the human developer is forced to act as a manual coordinator—copying context, resolving differences, and managing handoffs.

AIPass allows multiple agents to operate simultaneously within the same physical filesystem and repository. To prevent chaos, the framework establishes a set of coordination protocols directly inside the local directory.

### The Trinity Pattern Architecture

To provide agents with persistent identity and memory without overwhelming context windows, AIPass relies on the Trinity Pattern. This pattern stores structured data across three distinct, local, and git-diffable JSON files located within a `.trinity/` directory at the project root :

- **`id.json` (Identity)**: Defines the agent's core capabilities, structural boundaries, and persistent role description. This file remains unchanged to preserve the agent's core identity.
    
- **`local.json` (Session History)**: Records a rolling timeline of session activities, technical learnings, and current focal points. It utilizes a rolling First-In, First-Out (FIFO) extraction strategy to prune old logs and prevent context window explosion, while extracting and preserving key technical constraints across rollovers.
    
- **`observations.json` (Collaboration Memory)**: Maps communications, workflow preferences, and inter-agent trust patterns to track how the agent interacts with other specialists and the human developer.
    

### Coordination and Collision Avoidance

To prevent parallel agents from corrupting files in a shared-filesystem setup, AIPass implements a four-tiered coordination protocol :

1. **Upfront Flow Planning**: Before modifying any codebase files, agents must construct and agree upon a structured flow plan. This plan explicitly partitions files and phases of work, ensuring agents do not edit the same lines of code concurrently.
    
2. **Dispatch Blockers**: Agents are modeled as single-threaded processes that cannot exist in multiple sessions simultaneously. If an agent receives multiple commands, they are queued sequentially within local mailbox files rather than spawning concurrent, competing subprocesses.
    
3. **The Orchestrator Privilege**: The first agent initialized within a project is assigned the Orchestrator role. While standard specialist agents can write local files and draft commits, only the Orchestrator has the privilege to merge branches or execute git-level integrations.
    
4. **Repo-Wide Git Blocks**: When an active agent completes a task and compiles a pull request, it enforces a repository-wide git write lock. This prevents other agents from staging changes until the PR is successfully compiled and local state is updated.
    
5. **System Watchdog**: To transition from active monitoring to trustable autonomy, AIPass implements a background watchdog daemon. This process monitors background agent scripts and alerts the user immediately upon success, crash, or silent exit.
    

## Category 3: Git-Worktree Partitioning and Desktop Orchestration

Git-worktree isolation represents the most commercially dominant paradigm for parallel agent execution. Traditionally, if a developer wanted to run multiple agents in parallel on the same repository, they faced severe workspace conflicts. Running concurrent sessions within the same directory meant that one agent's uncommitted modifications would corrupt another agent's execution context, break build states, and result in logical merge conflicts before code could even be staged.

Git worktrees solve this by allowing multiple checked-out branches to coexist in separate physical directories on the local machine, all linked to a single underlying `.git` database. This structure allows developers to spawn multiple agent processes in parallel without duplicating heavy repository historical data, with creation times taking seconds rather than minutes.

### Under-the-Hood Mechanics of Git Worktrees

When an agent or developer invokes a worktree command—such as `git worktree add../feature-auth -b feature-auth` —Git creates a lightweight linked directory. This directory does not contain a standard `.git/` folder; instead, it contains a simple `.git` file with a single line pointing back to the main repository's database :

$$\text{gitdir: } /path/to/main/.git/worktrees/feature-auth$$

Git internally manages independent pointers for each worktree, setting the `$GIT_DIR` variable to isolate the active staging area (`.git/index`), the current branch head (`HEAD`), and transaction logs.

While Git worktrees solve workspace pollution, they introduce significant development friction in practice :

- **Port and Environment Collisions**: Parallel agents attempting to spin up local development servers will collide on default ports (e.g., both trying to bind to port 3000), causing execution crashes.
    
- **State Isolation Gaps**: While source files are partitioned, external services remain shared. If two agents write to the same local SQLite database or interact with the same shared Docker daemon, data corruption and race conditions occur.
    
- **Tooling and IDE Overhead**: Gitignored configuration files (such as local `.env` files) and virtual environments (such as Python `venv` or `node_modules`) are missing from newly created worktrees, requiring manual setup or custom pre-execution scripts. Furthermore, complex IDEs (like PyCharm) often lose project-specific settings when opening these dynamic directories, as configuration directories (e.g., `.idea`) are typically untracked.
    

To manage these issues, several tools have emerged to automate the creation, tracking, and teardown of these local environments.

### Emdash

Emdash is an open-source, Apache 2.0-licensed desktop Agentic Development Environment (ADE) backed by Y Combinator (W26). It acts as a visual command center for terminal-based agents, wrapping them in git-worktree isolation. Emdash is completely agent-agnostic. Upon startup, it automatically scans the system's execution path (`PATH`) to detect installed CLI tools, seamlessly integrating up to 28 different providers, including Claude Code, Codex, Cursor, and OpenCode.

The tool stores its internal application state within a local SQLite database, maintaining strict data privacy. It also supports native, full SSH/SFTP connections with proxying and key-forwarding, allowing developers to execute parallel worktree pipelines on remote production machines while managing everything through a local GUI.

### T3 Code

T3 Code is an open-source, Codex-first graphical user interface built on a "Bring Your Own Key" (BYOK) model to eliminate platform fees. Its user interface is split into a three-panel layout to streamline context management: the left sidebar handles repository paths, the center panel tracks active task threads, and the right panel renders the chat history, agent plans, and real-time tool calls.

T3 Code integrates Git worktrees directly into its UI, allowing users to configure reasoning levels (planning depths), toggle between chat and planning modes, and swap underlying models mid-session. It is highly optimized for the final stages of the development lifecycle, offering a turn-by-turn diff viewer and a one-click commit-push-PR pipeline that automatically generates commit messages, pushes branches to remote repositories, and opens a pre-populated GitHub PR page.

### Microsoft GitHub Copilot Desktop App

Unveiled in technical preview at the Microsoft Build 2026 conference, this desktop application targets enterprise-scale automated development. To manage the massive volume of agentic commits (which have nearly doubled year-over-year to over 1.4 billion monthly commits), the application provides a "My Work" dashboard to coordinate parallel agents.

Key features include **Canvases**—interactive visual panels displaying agent plans, terminal streams, and live deployments in real time—and **Agent Merge**, a utility that monitors CI pipelines, resolves testing failures, and automatically merges code once compliance rules are satisfied.

The secondary utilities in this category focus on lightweight, terminal-centric worktree automation:

|**Feature / Utility**|**worktree-cli**|**gwq**|**Worktrunk (wt)**|
|---|---|---|---|
|**Primary Interface**|Command Line Interface (CLI)|Terminal Dashboard (TUI)|Enhanced Command Line Interface (CLI)|
|**Target Integration**|Claude Code & MCP servers|Cross-repository monitoring|Claude Code & standalone terminal agents|
|**Boilerplate Automation**|Copies local `.env` and environment configs automatically.|None (focuses on execution tracking).|Toml-based hooks (`.worktrunk.toml`) to run post-create commands (e.g., package installs, DB seeding).|
|**Process Control**|Basic synchronous launch.|Tmux integration for persistent, long-running agent background tasks.|Integrated `switch`, `list`, `merge`, and `remove` commands to automate the worktree lifecycle.|
|**State Visualization**|Minimal directory pathing.|Unified terminal status dashboard across multiple project roots.|Rich status indicators tracking staged changes, branch age, commits, and PR linkages.|

## Category 4: Procedural Skill and Consensus Protocols

While containerized and worktree systems focus on physical and filesystem boundaries, another branch of harness engineering focuses on cognitive and behavioral constraints. These frameworks do not partition files; instead, they restrict the reasoning process of the agents to force adherence to software engineering methodologies.

### Obra Superpowers

Developed by Jesse Vincent and the team at Prime Radiant, Superpowers is an open-source, model-agnostic skills framework that acts as a strict procedural layer on top of platforms like Claude Code, Codex, and Cursor. Rather than allowing an LLM to immediately generate code upon receiving a user prompt, Superpowers utilizes system-start hooks to inject a master skill (`SKILL.md`) at the beginning of every execution session. This forces the agent through a four-phase workflow :

1. **Design-First Brainstorming**: The agent conducts a Socratic discussion with the user, raising edge cases and exploring architectural tradeoffs. It must compile a written design specification and receive explicit user approval before writing any code.
    
2. **Isolated Workspace**: The framework utilizes git worktrees to establish a clean branch and validates that the pre-existing test suite passes completely before modification.
    
3. **Granular Planning**: The agent writes a detailed implementation plan composed of tiny, 2-to-5-minute micro-tasks. Each task must include explicit file paths, expected code blocks, and precise verification commands. This level of over-specification forces the planning phase to resolve architectural ambiguity before any code is generated.
    
4. **Subagent-Driven Development with TDD**: To complete the plan, the host agent spawns highly focused, short-lived subagents. Each subagent receives only its specific task and a clean context window, eliminating the risk of context drift. The subagents operate under strict test-driven development (TDD) rules, writing a failing test first, writing the code to make it pass, and completing two peer-review gates (spec compliance and code quality) before merging.
    

### Practical Implementation: The Deep Cuts Case Study

A notable real-world application of behavioral skill harnesses occurred during the development of Deep Cuts, a local-first music intelligence desktop app built natively in Rust, Tauri, and Svelte 5. The developer and the AI agent established an autonomy-control spectrum guided by a custom `skills/` directory containing markdown protocols. These protocols documented how to handle database migrations, manage Svelte 5 state stores, and write Tauri IPC commands.

By encoding these preferences into persistent markdown files, the agents read them at the start of each session, removing the need to re-explain the architecture. The effectiveness of this approach was demonstrated when the agent, guided by the local design protocols, successfully challenged the developer's initial request for a pure LLM text search, proposing instead a faceted sidebar filter that ultimately became the core navigation pillar of the application.

### Academic and Distributed Consensus Harnesses

In the academic domain, harnesses are being designed to coordinate agents through formal mathematical consensus models. This approach is particularly valuable for distributed estimations, collaborative AI, and multi-agent systems operating under extreme noise or adversarial environments.

#### Multi Agent Cognition Protocol (MACP)

MACP introduces a shared, real-time cognitive bus that allows active agents to exchange current findings, priorities, and state updates during execution. This contrasts with standard workflows where agents only communicate at the end of a task, remaining blind to parallel changes mid-flight. This protocol is paired with the **Freight Agency Bench**—which measures agent planning and adaptation in dynamic environments—and **Pulse**, a persistent orchestration engine.

#### Mathematical Models of Consensus

In decentralized systems, classical multi-agent consensus protocols require independent agents, each starting with an initial proposal $x_i(0)$, to asymptotically converge on a shared value through local communication over a graph $G = (V, E)$. In continuous time, this is classically modeled as :

$$\dot x_i(t) = \sum_{j \in \mathcal{N}_i} a_{ij} \left(x_j(t) - x_i(t)\right)$$

In discrete-time systems, the state transition is represented as :

$$x_i(k+1) = x_i(k) + \sum_{j \in \mathcal{N}_i} a_{ij} \left(x_j(k) - x_i(k)\right)$$

To handle unreliable or adversarial agents, advanced harnesses implement robust algorithms like the **Mean-Subsequence-Reduced (MSR)** protocol. In an MSR-based harness, agents sort their neighbors' state values, discard the largest and smallest values (which are suspected to be faulty or adversarial), and compute updates using only the remaining values. To further enforce differential privacy of initial agent states, the **DP-MSR** algorithm augments this protocol with Laplace noise injection of geometrically decaying scale, balancing accuracy, security, and consensus convergence.

For multimodal systems, paradigms like **Group Cognition Learning (GCL)** organize multi-agent consensus into a structured two-stage protocol :

1. **Selective Interaction**: A Routing Agent and an Auditing Agent evaluate the marginal predictive gain of information exchanges, pruning fully connected communication networks into sample-specific, sparse topologies to prevent redundant coupling.
    
2. **Consensus Formation**: A Public-Factor Agent extracts a shared semantic signal from different modalities, allowing an Aggregation Agent to synthesize a final decision using contribution-aware weights while preserving the individual agent's specialized modality representation.
    

This structure-aware coordination becomes vital under extreme conditions; for instance, the **Diffusive Cognitive Consensus (DCC)** framework improves terminal accuracy by up to 13.9% over classical consensus in high-noise environments by triggering conditional diffusion only when multi-criteria gates detect state instability.

## Strategic Conclusions

The selection of a multi-agent harness architecture is a strategic decision that depends on security requirements, team velocity, and project scale.

For setups handling untrusted third-party code, open-source packages, or environments containing sensitive cloud credentials, the **Containerized Isolation** model is mathematically and architecturally necessary. No amount of application-level permission prompting can replace the security guarantees of a containerized sandbox that hard-blocks access to sensitive SSH and environment keys.

For high-velocity, trusted developer environments where parallel feature generation is the primary requirement, **Git-Worktree Partitioning** provides the ideal balance. This approach eliminates workspace collisions and branch confusion, allowing multiple agents to run concurrently while deferring merge conflicts to intentional, structured PR review stages.

Finally, for teams looking to build deeply integrated, long-term agentic installations that work on the same directory alongside human developers, the **Shared Workspace & Persistent Identity** paradigm offers a highly collaborative alternative. By substituting physical isolation with logical planning, file locks, and structured JSON-based memories, this approach allows agents to operate as cohesive, long-lived technical teams.

Ultimately, these various paradigms demonstrate that a horse's power only becomes useful when it is constrained; similarly, the non-deterministic capabilities of modern AI agents require the structured, deterministic boundaries of a well-engineered harness to achieve production-grade reliability.

# Q: Do any of the frameworks that you analyzed support self-improvement, or even better parallel recursive self-improvement (think MCTS, alphago / alphazero)?

When evaluating the landscape of multi-agent harnesses and developer environments, it is helpful to divide "self-improvement" into two distinct paradigms: **trace-based skill self-improvement** (which runs locally on top of developer harnesses) and **algorithm-driven reinforcement learning self-improvement** (which uses Monte Carlo Tree Search (MCTS) and training loops).

While standard Git-based developer harnesses like Hydra, Emdash, and T3 Code focus purely on workspace coordination rather than model training, several closely related frameworks and research stacks implement self-improvement and MCTS-style recursion.

### 1. Developer-Scale & Trace-Based Self-Improvement

At the local developer level, frameworks focus on modifying an agent’s **scaffolding, system instructions, or code** based on trace analysis of prior failures.

#### A. `recursive-improve` (Kayba AI)

The most direct implementation of a local-first self-improving loop is **`recursive-improve`**, an open-source framework designed to run on top of CLI-based coding agents like Claude Code or Codex.

- **Trace Capture:** By calling two lines of Python code, the framework monkey-patches standard LLM clients to record all token traces and tool executions locally.
    
- **The `/recursive-improve` Skill:** An agent analyzes these trace logs, looks for cognitive patterns (such as infinite loops, silent failures, or repeated tool hallucinations), plans code or prompt-level fixes, and applies them to its own repository.
    
- **The `/ratchet` Autonomous Loop:** Highly analogous to a simplified recursive search, the framework has a "ratchet" state where the agent runs a continuous, overnight loop of **Improve $\to$ Run Agent $\to$ Evaluate $\to$ Keep or Revert**. Only modifications that successfully pass a local dynamic evaluation suite survive, systematically climbing a performance gradient.
    

#### B. `hermes-agent`

Unlike TDD-based platforms, **`hermes-agent`** is architected around "dynamic skill accumulation". Instead of rewriting its primary source files, the agent discovers and formalizes reusable code routines, aggregates them as an evolving database of execution "skills," and calls upon this learned library to solve progressively harder tasks without requiring core prompt edits.

#### C. `AIPass`

In the **`AIPass`** local framework (built on the Trinity memory pattern), agents utilize persistent local memory JSONs (`.trinity/`) that act as a rolling FIFO cache of past mistakes, project context, and structural learnings. Rather than treating every run as a stateless interaction, the agent begins each task by reading its historical execution logs. Furthermore, AIPass is "self-maintained"—the agents write pull requests to upgrade the very CLI tools and schemas that run them, establishing a practical human-in-the-loop self-improvement cycle.

### 2. Parallel Recursive Self-Improvement & MCTS (AlphaGo/AlphaZero Style)

For true mathematical self-improvement—where a system acts as both generator and evaluator to train itself via tree search and reinforcement learning—we must look to the MLOps and academic research tier.

#### A. `ReST-MCTS*` (Reinforced Self-Training MCTS)

To replicate the AlphaGo/AlphaZero loop for code and reasoning agents, researchers utilize **`ReST-MCTS*`**.

- **The Core Mechanism:** Rather than relying on static prompting, this framework integrates **Monte Carlo Tree Search (MCTS)** with Process Reward Models (PRMs) to evaluate logical paths at a step-by-step level.
    
- **Inferring Process Rewards:** To bypass the bottleneck of human annotation, `ReST-MCTS*` acts like AlphaGo's value network. Given a final correct answer (the oracle), the tree-search algorithm estimates the probability that a specific intermediate reasoning step will lead to that correct solution.
    
- **Recursive Optimization:** The high-quality reasoning paths mapped by the MCTS are funneled back into the system to fine-tune both the policy model (the generator) and the value model (the evaluator) recursively across multiple training iterations. It is designed to scale mathematically, ensuring the model's reasoning capabilities compound without human intervention.
    

#### B. `agent-lightning`

Built to address reinforcement learning at the agentic level, **`agent-lightning`** focuses on optimizing the policy weights of an agent through environmental RL. It treats the workspace (terminal execution, compiler outcomes, and browser sandboxes) as the environment, utilizing active reward signals to train the agent's decision-making algorithms over thousands of parallel runs.

### The Practical Bottlenecks of Recursive Agent Loops

While recursive loops sound powerful in theory, developers and researchers working with these systems report three critical bottlenecks in practice:

1. **Context Bloat:** Accumulating raw execution traces over multiple generations quickly floods the context window with old failure logs. To maintain speed and accuracy, systems must aggressively synthesize and summarize learnings at write-time rather than feeding raw logs back into the model.
    
2. **Cascading Regressions & Over-Correction:** In a pure "keep-or-revert" hill-climbing loop (like `/ratchet`), an agent trying to fix Failure $A$ will often over-index on that failure, introducing a subtle regression that breaks previously-working Feature $B$. To prevent this, the evaluation engine must have incredibly comprehensive test coverage.
    
3. **Complexity Overhead:** Developers frequently observe that building and maintaining complex RL and MCTS evaluation infrastructure consumes more engineering hours than simply refining the agent's prompts and Git boundary constraints in a standard harness like Hydra or Emdash.

Here are the links to the official GitHub repositories and websites for the frameworks and developer tools mentioned in the analysis:

### 1. Developer-Scale & Trace-Based Self-Improvement

- **`recursive-improve` (Kayba AI)**
    
    - **GitHub Repository:** [kayba-ai/recursive-improve](https://github.com/kayba-ai/recursive-improve)
        
        [cite: 1]
        
- **`hermes-agent` (Nous Research)**
    
    - **GitHub Repository:** [NousResearch/hermes-agent](https://github.com/nousresearch/hermes-agent)
        
        [cite: 2]
        
    - **Official Website:** [hermes-agent.org](https://hermes-agent.org/)
        
        [cite: 3]
        
- **`AIPass` (AIOSAI)**
    
    - **GitHub Repository:** [AIOSAI/AIPass](https://github.com/AIOSAI/AIPass)
        
        [cite: 4]
        

### 2. Parallel Recursive Self-Improvement & MCTS

- **`ReST-MCTS*` (THUDM)**
    
    - **GitHub Repository:** [THUDM/ReST-MCTS](https://github.com/THUDM/ReST-MCTS)
        
        [cite: 5]
        
- **`agent-lightning` (Microsoft)**
    
    - **GitHub Repository:** [microsoft/agent-lightning](https://github.com/microsoft/agent-lightning)
        
        [cite: 6]
        
    - **Documentation & Website:** [agent-lightning Documentation](https://microsoft.github.io/agent-lightning/)
        
        [cite: 7]
        

### 3. Local Workspace Coordination Harnesses

- **Deep Cuts (from which Hydra is extracted)**
    
    - **GitHub Repository:** [robertolupi/deep-cuts](https://github.com/robertolupi/deep-cuts)
        
        [cite: 8]
        
        _(Note: As noted in the article, Hydra is the extracted protocol and has not yet been officially released as a standalone open-source repository)._
        
- **`Emdash` (General Action)**
    
    - **GitHub Repository:** [generalaction/emdash](https://github.com/generalaction/emdash)
        
        [cite: 8]
        
- **`T3 Code`**
    
    - **GitHub Repository:** [pingdotgg/t3code](https://github.com/pingdotgg/t3code)
        
        [cite: 8]