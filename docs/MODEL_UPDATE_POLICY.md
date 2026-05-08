# Model Update Policy

## Overview

MiMo-TUI uses a three-channel model governance system to safely manage model updates.

## Channels

| Channel | Description | Gating |
|---------|-------------|--------|
| `default` | Production-ready models | None - always available |
| `candidate` | Models under evaluation | Warning on use |
| `labs` | Experimental models | Requires `MIMO_LABS=1` |

## Model Promotion Workflow

### Step 1: Register as Candidate
New models are registered in the `candidate` channel with `Accepted=false`.

### Step 2: Golden Session Recording
Record a golden session with the current default model:
```bash
mimo -session golden-v1 -golden-session golden-v1
```

### Step 3: Candidate Evaluation
Run the candidate model against the same tasks:
```bash
mimo -session candidate-v1 -model-accept mimo-v2.5-flash -candidate-session candidate-v1
```

### Step 4: Replay Gate
The replay gate compares:
- Trajectory completeness
- Tool call accuracy
- Error rate
- Context management quality

### Step 5: Accept or Reject
If the candidate passes the gate:
```bash
mimo -model-accept mimo-v2.5-flash
```

This promotes the candidate to `default` channel and sets `Accepted=true`.

### Step 6: Rollback (if needed)
If issues are discovered after promotion:
```bash
mimo -model-accept mimo-v2.5-pro  # Restore previous default
```

## Pre-seeded Models

| Model | Channel | Context | Status |
|-------|---------|---------|--------|
| mimo-v2.5-pro | default | 1M | Accepted |
| mimo-v2.5-flash | candidate | 128K | Pending |
| mimo-v2-pro | candidate | 256K | Pending |

## Safety Guarantees

1. **No automatic promotion** - Candidates must pass the replay gate
2. **Rollback capability** - Previous default can be restored
3. **Channel isolation** - Labs models require explicit opt-in
4. **Warning on candidates** - Users are notified when using unaccepted models
5. **Audit trail** - All model changes are logged

## CLI Flags

- `-list-models`: Print all registered models
- `-model-accept <id>`: Accept a candidate model
- `-candidate-session <id>`: Session to evaluate against golden
- `-golden-session <id>`: Mark a session as golden
