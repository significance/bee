# Round Skipping Near Depth Changes in Swarm Bee

## Overview

This document explains the mechanism by which Swarm Bee nodes skip participation in storage incentives redistribution rounds when depth changes occur in the network topology. This behavior is a critical part of the protocol that ensures consistency, correctness, and network integrity during dynamic topology changes.

## Table of Contents

1. [Core Concepts](#core-concepts)
2. [Key Components](#key-components)
3. [The Round Skipping Decision Flow](#the-round-skipping-decision-flow)
4. [Why Depth Changes Cause Round Skipping](#why-depth-changes-cause-round-skipping)
5. [Topology Depth Management](#topology-depth-management)
6. [The Sampling Process](#the-sampling-process)
7. [Protocol Analysis and Recommendations](#protocol-analysis-and-recommendations)

---

## Core Concepts

### Committed Depth

The **committed depth** is the effective storage responsibility depth at which a node commits to store chunks during a redistribution round. It is calculated as:

```
CommittedDepth = StorageRadius + CapacityDoubling
```

**Source:** `pkg/storer/reserve.go:422-428`

```go
func (db *DB) CommittedDepth() uint8 {
	if db.reserve == nil {
		return 0
	}

	return uint8(db.reserveOptions.capacityDoubling) + db.reserve.Radius()
}
```

Where:
- **StorageRadius**: The current neighborhood depth from Kademlia topology (the minimum proximity at which the node stores chunks)
- **CapacityDoubling**: A constant factor representing how much the node has expanded its storage responsibility area beyond its current radius

### Storage Radius

The storage radius represents the neighborhood depth boundary. A node is responsible for storing all chunks with proximity greater than or equal to this radius relative to the node's base address.

### Round Participation

Nodes participate in redistribution rounds to prove they are storing chunks correctly and earn rewards. However, participation is conditional based on:
1. Node state (frozen, synced, healthy)
2. Neighborhood selection (depth-dependent)
3. Available funds

---

## Key Components

### 1. The IsPlaying Smart Contract Check

The core mechanism for determining round participation based on depth.

**Source:** `pkg/storageincentives/redistribution/redistribution.go:69-87`

```go
// IsPlaying checks if the overlay is participating in the upcoming round.
func (c *contract) IsPlaying(ctx context.Context, depth uint8) (bool, error) {
	callData, err := c.incentivesContractABI.Pack("isParticipatingInUpcomingRound", c.owner, depth)
	if err != nil {
		return false, err
	}

	result, err := c.callTx(ctx, callData)
	if err != nil {
		return false, fmt.Errorf("IsPlaying: owner %v depth %d: %w", c.owner, depth, err)
	}

	results, err := c.incentivesContractABI.Unpack("isParticipatingInUpcomingRound", result)
	if err != nil {
		return false, fmt.Errorf("IsPlaying: results %v: %w", results, err)
	}

	return results[0].(bool), nil
}
```

**Key Points:**
- This function queries the on-chain redistribution smart contract
- It passes the node's owner address and the **committed depth**
- The contract determines if the node's neighborhood (at that depth) is selected for the upcoming round
- Different depths result in different neighborhood selections

### 2. Topology Depth Calculation

The Kademlia topology continuously recalculates depth based on peer connectivity.

**Source:** `pkg/topology/kademlia/kademlia.go:889-957`

```go
// recalcDepth calculates the depth of the kademlia based on the connection count
// of each bin. The depth is the shallowest bin where the node has fewer than
// saturationPeers connected peers.
func (k *Kad) recalcDepth() (uint8, error) {
	k.depthMu.Lock()
	defer k.depthMu.Unlock()

	// ... initialization code ...

	var (
		peerCount           = 0
		candidateDepth      = uint8(0)
		shallowestEmptyBin  = -1
		deepestNonEmptyBin  = -1
	)

	// iterate from deepest bin to shallowest
	for i := int(swarm.MaxPO); i >= 0; i-- {
		binConnectedPeers := k.connectedBins[i]
		peerCount += binConnectedPeers

		if binConnectedPeers > 0 {
			if deepestNonEmptyBin == -1 {
				deepestNonEmptyBin = i
			}
		}

		if peerCount >= k.opt.SaturationPeers {
			candidateDepth = uint8(i)
			break
		} else {
			if binConnectedPeers == 0 {
				shallowestEmptyBin = i
			}
		}
	}

	// ... depth validation logic ...

	return depth, nil
}
```

**Depth Changes Occur When:**
- A peer connects (`connectionAttemptsHandler` → `recalcDepth()` at line 459)
- A peer disconnects (line 1219)
- Peer reachability changes (line 1339)

These events can shift the storage radius, which in turn affects the committed depth.

---

## The Round Skipping Decision Flow

The complete decision logic is in the `handleSample` function.

**Source:** `pkg/storageincentives/agent.go:392-446`

```go
func (a *Agent) handleSample(ctx context.Context, round uint64) (bool, error) {
	// Get the minimum proximity between the anchor and stored chunks
	committedDepth := a.store.CommittedDepth()

	// Check 1: Node Frozen
	if a.state.IsFrozen() {
		a.logger.Info("skipping round because node is frozen")
		return false, nil
	}

	// Check 2: Smart Contract Neighborhood Selection (DEPTH DEPENDENT)
	isPlaying, err := a.contract.IsPlaying(ctx, committedDepth)
	if err != nil {
		a.metrics.ErrCheckIsPlaying.Inc()
		return false, err
	}
	if !isPlaying {
		a.logger.Info("not playing in this round")
		return false, nil
	}

	// Check 3: Node Synchronization
	if !a.state.IsFullySynced() {
		a.logger.Info("skipping round because node is not fully synced")
		return false, nil
	}

	// Check 4: Node Health
	if !a.state.IsHealthy() {
		a.logger.Info("skipping round because node is unhealthy", "round", round)
		return false, nil
	}

	// Check 5: Sufficient Funds
	_, hasFunds, err := a.HasEnoughFundsToPlay(ctx)
	if err != nil {
		return false, fmt.Errorf("has enough funds to play: %w", err)
	} else if !hasFunds {
		a.logger.Info("insufficient funds to play in next round", "round", round)
		a.metrics.InsufficientFundsToPlay.Inc()
		return false, nil
	}

	// If all checks pass, generate and store sample
	now := time.Now()
	sample, err := a.makeSample(ctx, committedDepth)
	if err != nil {
		return false, err
	}
	dur := time.Since(now)
	a.metrics.SampleDuration.Set(dur.Seconds())

	a.logger.Info("produced sample", "hash", sample.ReserveSampleHash, "radius", committedDepth, "round", round)

	a.state.SetSampleData(round, sample, dur)

	return true, nil
}
```

### Decision Criteria (in order):

1. **Is the node frozen?** → Skip if yes
2. **Is the node's neighborhood selected at the current committed depth?** → Skip if no (depth-dependent)
3. **Is the node fully synced?** → Skip if no
4. **Is the node healthy?** → Skip if no
5. **Does the node have sufficient funds?** → Skip if no

If all checks pass, the node generates a sample and participates in the round.

---

## Why Depth Changes Cause Round Skipping

### The Documentation Explains It Best

**Source:** `pkg/storer/sample.go:45-60`

```go
// ReserveSample generates the sample of reserve storage of a node required for the
// storage incentives agent to participate in the lottery round. In order to generate
// this sample we need to iterate through all the chunks in the node's reserve and
// calculate the transformed hashes of all the chunks using the anchor as the salt.
//
// If the node has doubled their capacity by some factor, sampling process need to only
// pertain to the chunks of the selected neighborhood as determined by the anchor and the
// "committed depth" and NOT the whole reserve.
//
// The committed depth is the sum of the radius and the doubling factor.
// For example, the committed depth is 11, but the local node has a doubling factor of 3,
// so the local radius will eventually drop to 8. The sampling must only consider chunks
// with proximity 11 to the anchor.
```

### The Mechanism

When a node's depth changes, the following cascade occurs:

```
Peer Connects/Disconnects
    ↓
Kademlia recalcDepth()
    ↓
StorageRadius changes
    ↓
CommittedDepth changes (StorageRadius + CapacityDoubling)
    ↓
Agent calls IsPlaying(newCommittedDepth)
    ↓
Smart contract evaluates neighborhood selection at NEW depth
    ↓
Contract may return false (neighborhood not selected at this depth)
    ↓
Node skips round
```

### Why This Matters

1. **Neighborhood Selection is Depth-Specific**: The smart contract uses a deterministic algorithm to select which neighborhoods participate in each round. This selection is based on the anchor hash and the depth. When a node's committed depth changes, it may move from a selected neighborhood to a non-selected one (or vice versa).

2. **Sample Correctness**: The sample must only include chunks that match the committed depth. If depth changes mid-round, a previously generated sample would be invalid.

3. **Protocol Consistency**: All nodes must agree on who is participating. If a node's depth changes, other nodes querying the contract with the new depth would get the correct participation status.

### Example Scenario

```
Initial State:
- StorageRadius = 8
- CapacityDoubling = 3
- CommittedDepth = 11
- IsPlaying(11) = true → Node participates

Peer Connects in Bin 8:
- StorageRadius = 9 (depth increased)
- CapacityDoubling = 3
- CommittedDepth = 12
- IsPlaying(12) = false → Node skips round

Why? The node is now in a different neighborhood at depth 12,
and that neighborhood is not selected for this round.
```

---

## The Sampling Process

When a node does participate, it must generate a sample that respects the committed depth boundary.

**Source:** `pkg/storer/sample.go:106-119`

```go
// Phase 1: Iterate chunk addresses
g.Go(func() error {
	start := time.Now()
	stats := SampleStats{}
	defer func() {
		stats.IterationDuration = time.Since(start)
		close(chunkC)
		addStats(stats)
	}()

	// Only iterate chunks starting from StorageRadius
	err := db.reserve.IterateChunksItems(db.StorageRadius(), func(ch *reserve.ChunkBinItem) (bool, error) {
		// Only include chunks with proximity >= committedDepth to anchor
		if swarm.Proximity(ch.Address.Bytes(), anchor) < committedDepth {
			return false, nil
		}
		select {
		case chunkC <- ch:
			stats.TotalIterated++
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	return err
})
```

**Key Filter** (line 107):
```go
if swarm.Proximity(ch.Address.Bytes(), anchor) < committedDepth {
	return false, nil
}
```

This ensures only chunks within the committed depth boundary are included in the sample. This is critical because:
- The sample represents the node's proof of storage for a specific neighborhood depth
- Including chunks outside the committed depth would be incorrect
- The smart contract validation expects samples that match the declared depth

### Neighborhood Statistics

The system tracks statistics per neighborhood at the committed depth.

**Source:** `pkg/storer/reserve.go:516-539`

```go
type NeighborhoodStat struct {
	Neighborhood            swarm.Neighborhood
	ReserveSizeWithinRadius int
	Proximity               uint8
}

func (db *DB) NeighborhoodsStat(ctx context.Context) ([]*NeighborhoodStat, error) {
	radius := db.StorageRadius()
	committedDepth := db.CommittedDepth()

	// Create neighborhood stats for all possible prefixes at committed depth
	for i, n := range prefixes {
		neighs[i] = &NeighborhoodStat{
			Neighborhood:            swarm.NewNeighborhood(n, committedDepth),
			ReserveSizeWithinRadius: 0,
			Proximity:               min(committedDepth, swarm.Proximity(n.Bytes(), db.baseAddr.Bytes())),
		}
	}

	// Count chunks that match committed depth
	// ...
	if swarm.Proximity(ch.Address.Bytes(), n.Neighborhood.Bytes()) >= committedDepth {
		n.ReserveSizeWithinRadius++
	}
}
```

---

## Topology Depth Management

### When Depth Changes Occur

The depth recalculation is triggered by connection events:

**Source:** `pkg/topology/kademlia/kademlia.go:459` (peer connect)
```go
func (k *Kad) connectionAttemptsHandler(ctx context.Context) {
	// ... connection handling ...
	depth, changed, err := k.recalcDepth()
	// ... handle depth change ...
}
```

**Source:** `pkg/topology/kademlia/kademlia.go:1219` (peer disconnect)
```go
func (k *Kad) disconnected(ctx context.Context, addr swarm.Address) {
	// ... disconnect handling ...
	depth, changed, err := k.recalcDepth()
	// ... handle depth change ...
}
```

### Depth Calculation Algorithm

The algorithm finds the shallowest bin where the node has fewer than `SaturationPeers` (default 8) connected peers:

1. **Iterate from deepest to shallowest bin**
2. **Count total connected peers**
3. **When count reaches SaturationPeers, mark that bin as candidate depth**
4. **Check for empty bins shallower than candidate**
5. **Return final depth**

This means:
- More peers connecting in deeper bins → depth increases
- Peers disconnecting → depth may decrease
- Empty bins can affect the final depth calculation

---

## Test Coverage

The behavior is validated in tests:

**Source:** `pkg/storageincentives/agent_test.go:113-116`

```go
var radius uint8 = 8

contract := &mockContract{t: t, expectedRadius: radius + tc.doubling}
```

This confirms that the agent uses `radius + doubling` (CommittedDepth) when calling `IsPlaying`.

---

## Related Protocol Evolution

### Recent Changes

**Commit:** `e7bc895b` (Oct 15, 2024)
**Title:** "fix(redistribution): reveal with correct depth, swip21"

This fix ensured that the reveal phase uses CommittedDepth instead of just StorageRadius, maintaining consistency across the entire redistribution protocol.

**Commit:** `fb6c2e83` (Nov 5, 2024)
**Title:** "feat: add commited depth field to status protocol"

This added CommittedDepth to the status protocol, allowing peers to exchange and verify their committed depths.

---

## Analysis: When Nodes Abstain from Playing During Depth Changes

### The Abstention Mechanism

When a node's depth changes, it may abstain from participating in redistribution rounds even though it previously would have participated. This abstention is **deterministic** and based on smart contract neighborhood selection at the new depth.

### The Complete Abstention Flow

```
SCENARIO: Node was playing, then depth changes

Initial State (Round N):
├─ StorageRadius = 8
├─ CapacityDoubling = 3
├─ CommittedDepth = 11
├─ Node neighborhood at depth 11: "00001010100" (11 bits)
├─ IsPlaying(11) = TRUE
└─ Result: Node participates, generates sample, can win rewards

Depth Change Event:
├─ Peer connects in bin 8
├─ Kademlia recalcDepth() triggers
├─ StorageRadius increases: 8 → 9
└─ CommittedDepth increases: 11 → 12

New State (Round N+1):
├─ StorageRadius = 9
├─ CapacityDoubling = 3 (unchanged)
├─ CommittedDepth = 12
├─ Node neighborhood at depth 12: "000010101000" (12 bits - DIFFERENT!)
├─ IsPlaying(12) = FALSE
└─ Result: Node ABSTAINS, logs "not playing in this round"
```

### Why Different Depths Result in Different Participation

The smart contract uses a deterministic selection algorithm that evaluates:

```
isParticipatingInUpcomingRound(nodeAddress, depth) {
    1. Get current round anchor (hash)
    2. Calculate node's neighborhood prefix at specified depth
    3. Evaluate: Hash(anchor + depth + neighborhoodPrefix) vs. threshold
    4. Return: true if neighborhood selected, false otherwise
}
```

**Critical Insight:** A node's neighborhood **changes with depth**:
- Depth 11: Node represents 11-bit prefix (e.g., `00001010100`)
- Depth 12: Node represents 12-bit prefix (e.g., `000010101000`)
- These are **different neighborhoods** in the contract's selection logic
- Each round's anchor selects different neighborhoods at different depths

### Abstention Decision Point

**Source:** `pkg/storageincentives/agent.go:171-179`

```go
// Check 2: Smart Contract Neighborhood Selection (DEPTH DEPENDENT)
isPlaying, err := a.contract.IsPlaying(ctx, committedDepth)
if err != nil {
    a.metrics.ErrCheckIsPlaying.Inc()
    return false, err
}
if !isPlaying {
    a.logger.Info("not playing in this round")  // ← ABSTENTION LOG
    return false, nil
}
```

This is where abstention occurs:
1. Agent calls `IsPlaying` with **current** committed depth
2. Smart contract evaluates neighborhood selection at that depth
3. If neighborhood not selected → immediate return with `false`
4. Node skips all subsequent checks and does not generate a sample

### When Abstention Occurs

**Trigger Events:**

```go
// Depth recalculation triggered by:
1. Peer Connection    (kademlia.go:459)  → connectionAttemptsHandler()
2. Peer Disconnection (kademlia.go:1219) → disconnected()
3. Peer Reachability  (kademlia.go:1339) → peer reachability change
```

**Abstention Happens When:**

| Condition | Result |
|-----------|--------|
| Depth increases (more peers) | Old neighborhood → New neighborhood → May not be selected |
| Depth decreases (fewer peers) | Old neighborhood → New neighborhood → May not be selected |
| Depth unchanged | Same neighborhood → Selection status unchanged |

### Concrete Example with Real Values

```
Network Scenario:

Round 42, Anchor: 0xabcd1234567890...

Node A:
├─ Base Address: 0x00a1b2c3d4e5f6...
├─ StorageRadius: 8
├─ CommittedDepth: 11
├─ Neighborhood at depth 11: 0000 1010 100 (first 11 bits)
└─ IsPlaying(11) → Smart contract evaluates:
    - Hash(0xabcd...||11||00001010100) = 0x0045...
    - Threshold: 0x0080...
    - 0x0045 < 0x0080 → TRUE, neighborhood selected
    - Node PARTICIPATES ✓

Event: 3 peers connect in bin 8
├─ recalcDepth() executes
└─ StorageRadius: 8 → 9, CommittedDepth: 11 → 12

Round 43, Anchor: 0xef567890abcdef...

Node A (after depth change):
├─ Base Address: 0x00a1b2c3d4e5f6... (same)
├─ StorageRadius: 9
├─ CommittedDepth: 12
├─ Neighborhood at depth 12: 0000 1010 1000 (first 12 bits - DIFFERENT!)
└─ IsPlaying(12) → Smart contract evaluates:
    - Hash(0xef56...||12||000010101000) = 0x00b2...
    - Threshold: 0x0080...
    - 0x00b2 > 0x0080 → FALSE, neighborhood NOT selected
    - Node ABSTAINS ✗
    - Logs: "not playing in this round"
```

### The Smart Contract Selection Algorithm

The external package `github.com/ethersphere/go-storage-incentives-abi v0.9.4` implements the selection logic. Based on the code, the algorithm likely:

1. **Defines neighborhoods by depth:**
   - At depth D, there are 2^D possible neighborhoods
   - Each neighborhood is a D-bit prefix

2. **Selects a subset each round:**
   - Using the round anchor as a seed
   - Deterministically selects which neighborhoods participate
   - Selection percentage may be constant (e.g., 1/8 of all neighborhoods)

3. **Evaluates node participation:**
   - Given node address and depth, calculates which neighborhood it belongs to
   - Checks if that neighborhood is in the selected set for this round
   - Returns boolean result

### Neighborhood Selection Across Depths

**Example with 4 nodes at different depths:**

```
Round N, Anchor: 0xaaa...

Node 1: Depth 10 → Neighborhood 0000101010 → Selected ✓
Node 2: Depth 10 → Neighborhood 1110011101 → NOT selected ✗
Node 3: Depth 11 → Neighborhood 00001010100 → Selected ✓
Node 4: Depth 11 → Neighborhood 11100111011 → NOT selected ✗

Key Observation:
- Node 1 at depth 10: neighborhood "0000101010" → PLAYS
- Node 3 at depth 11: neighborhood "00001010100" → PLAYS
- If Node 1's depth changes to 11, it becomes "00001010101"
  → This is DIFFERENT from "00001010100"
  → May NOT be selected even though depth 10 was selected
```

### Abstention Frequency and Volatility

**High Volatility Scenarios:**

1. **Near Saturation Boundary:**
   - Node has exactly 8 peers in deepest saturated bin
   - One peer disconnect → depth decreases
   - One peer connect → depth increases
   - Results in rapid depth oscillation → frequent abstention changes

2. **Network Churn:**
   - High peer connection/disconnection rate
   - Continuous depth recalculation
   - Unpredictable participation status

3. **Bootstrap Phase:**
   - New nodes joining network
   - Rapidly changing peer count
   - Depth unstable until saturation achieved

**Stable Participation Scenarios:**

1. **Well-Saturated Node:**
   - Multiple peers beyond saturation in several bins
   - Depth changes rare
   - Consistent participation status

2. **Low Churn Network:**
   - Stable peer connections
   - Infrequent depth changes
   - Predictable rewards

### Observing Abstention in Practice

**Log Messages:**

```bash
# Neighborhood NOT selected (abstention)
INFO not playing in this round

# Neighborhood selected (will attempt to participate)
INFO neighbourhood chosen round=<N>
```

**Metrics:**

```go
// Tracked in pkg/storageincentives/metrics.go
NeighborhoodSelected counter    // Increments only when IsPlaying returns true
ErrCheckIsPlaying counter        // Increments on IsPlaying errors
```

**Debug Commands:**

```bash
# Check current committed depth
curl http://localhost:1635/readiness | jq '.storageRadius'

# Monitor depth changes in logs
tail -f bee.log | grep -E "(depth changed|not playing|neighbourhood chosen)"
```

### Why This Design?

**Protocol Benefits:**

1. **Deterministic Consensus:** All nodes agree on who should participate at each depth
2. **Depth-Scoped Responsibility:** Nodes only sample chunks within their committed neighborhood
3. **Dynamic Adaptation:** As topology changes, participation automatically adjusts
4. **Proof Validity:** Samples are always valid for the committed depth at generation time

**Trade-offs:**

1. **Participation Volatility:** Depth changes can cause unexpected abstention
2. **Reward Unpredictability:** Node operators can't predict when they'll skip rounds
3. **Wasted Preparation:** Nodes may prepare but abstain if depth changes
4. **Gaming Potential:** Sophisticated operators might manipulate peer connections

### Abstention vs. Other Skip Reasons

**Abstention is specifically:** `IsPlaying(committedDepth) returns false`

**Other skip reasons:**

```go
// Not abstention - these are secondary checks after IsPlaying returns true
if a.state.IsFrozen() {
    return false, nil  // Node frozen (penalty state)
}
if !a.state.IsFullySynced() {
    return false, nil  // Still syncing reserve
}
if !a.state.IsHealthy() {
    return false, nil  // Health check failed
}
if !hasFunds {
    return false, nil  // Insufficient BZZ tokens
}
```

**Key Difference:** Abstention happens **before** these checks. If `IsPlaying` returns false, the node never evaluates health, sync status, or funds. It's the **primary gate** for participation.

---

## Protocol Analysis and Recommendations

### How This Works: Critical Analysis

The round-skipping mechanism near depth changes is a **sophisticated but necessary protocol feature** that addresses several fundamental challenges in a dynamic, decentralized storage network:

#### Strengths

1. **Deterministic Participation**: By tying participation to on-chain neighborhood selection at a specific depth, the protocol ensures all nodes agree on who should participate, even as topologies shift. This prevents disputes and maintains consensus.

2. **Depth-Scoped Sampling**: The committed depth mechanism elegantly handles the transition period when nodes are expanding their storage responsibility. Nodes can commit to a deeper neighborhood while their actual storage radius is still shallowing, allowing smooth capacity scaling.

3. **Protection Against Invalid Proofs**: Skipping rounds during depth transitions prevents nodes from submitting samples that would be invalid under the new depth, which could result in slashing or lost rewards.

4. **Network Stability**: The multiple checks (frozen, synced, healthy, funds) create a robust gate-keeping system that ensures only ready nodes participate, improving overall protocol reliability.

#### Weaknesses and Concerns

1. **Participation Volatility**: Nodes near saturation boundaries (e.g., bin with exactly 8 peers) may experience frequent depth changes as peers connect and disconnect. This creates participation volatility where a node rapidly alternates between playing and not playing, leading to:
   - Unpredictable reward streams for node operators
   - Wasted computational resources generating samples that are never submitted
   - Potential gaming where nodes manipulate peer connections to skip unfavorable rounds

2. **Sample Generation Waste**: The current implementation appears to generate samples (expensive operation) before checking `IsPlaying`. If the depth changes between sample generation and submission, the sample is wasted. **Looking at the code, `IsPlaying` is checked BEFORE `makeSample`, which is correct**, but the concern remains if depth changes during the sample generation itself (though the duration metrics suggest this is fast enough to be unlikely).

3. **Lack of Depth Change Predictability**: Nodes have no advance warning that their depth is about to change. They learn about it reactively when `recalcDepth()` is triggered. This prevents proactive optimization strategies like:
   - Pre-generating samples for likely depth values
   - Deferring peer connections until after a round completes
   - Communicating to operators that rewards may be interrupted

4. **Capacity Doubling Rigidity**: The `CapacityDoubling` parameter is set at node initialization and appears to be static. This prevents dynamic adjustment strategies where nodes could temporarily reduce their committed depth during high churn periods to maintain participation.

5. **No Hysteresis in Depth Calculation**: The depth calculation immediately responds to every peer connection/disconnection. A small hysteresis band (e.g., requiring depth to be stable for N seconds, or requiring a change threshold of 2+ peers) could reduce participation volatility without harming the protocol.

### Recommendations for Protocol Improvement

#### 1. Depth Change Dampening

Implement a time-based or threshold-based dampening mechanism:

```go
// Pseudocode
func (k *Kad) recalcDepth() (uint8, error) {
    newDepth := calculateDepth()

    // Only apply change if:
    // - Depth differs by more than threshold (e.g., 2), OR
    // - Depth has been stable at new value for dampening period (e.g., 60s)

    if abs(newDepth - k.currentDepth) >= depthChangeThreshold {
        return newDepth, true
    }

    if newDepth != k.pendingDepth {
        k.pendingDepthTime = time.Now()
        k.pendingDepth = newDepth
        return k.currentDepth, false
    }

    if time.Since(k.pendingDepthTime) >= depthDampeningPeriod {
        return newDepth, true
    }

    return k.currentDepth, false
}
```

**Benefits:**
- Reduces participation volatility for nodes near saturation boundaries
- Allows temporary peer disconnections to recover without triggering depth changes
- More predictable rewards for operators

**Trade-offs:**
- Slightly slower response to genuine network topology changes
- Increased code complexity

#### 2. Predictive Depth Notification

Emit events or metrics when depth is likely to change soon:

```go
// Add to Kademlia
func (k *Kad) DepthChangeRisk() (risk float64, direction string) {
    peersUntilNextBin := k.opt.SaturationPeers - k.connectedBins[k.depth]

    if peersUntilNextBin <= 2 {
        return 0.8, "increase"
    }

    peersUntilPreviousBin := k.countPeersIn(k.depth + 1)
    if peersUntilPreviousBin < k.opt.SaturationPeers * 0.5 {
        return 0.6, "decrease"
    }

    return 0.0, "stable"
}
```

**Benefits:**
- Operators can anticipate reward interruptions
- Agents could potentially pre-generate samples for likely depths
- Better observability and debugging

#### 3. Grace Period for Depth Changes Mid-Round

If a depth change occurs after a sample has been generated but before submission, allow the node to use the sample with the old depth if it was valid when generated:

```go
func (a *Agent) handleSample(ctx context.Context, round uint64) (bool, error) {
    initialDepth := a.store.CommittedDepth()

    isPlaying, err := a.contract.IsPlaying(ctx, initialDepth)
    if !isPlaying {
        return false, nil
    }

    sample, err := a.makeSample(ctx, initialDepth)
    if err != nil {
        return false, err
    }

    // Check if depth changed during sampling
    currentDepth := a.store.CommittedDepth()
    if currentDepth != initialDepth {
        // Grace period: Still submit if initial depth was valid
        a.logger.Info("depth changed during sampling, submitting with initial depth",
            "initial", initialDepth, "current", currentDepth)
    }

    a.state.SetSampleData(round, sample, dur)
    return true, nil
}
```

**Benefits:**
- Reduces wasted sample generation
- Increases participation rate during topology churn
- Maintains protocol correctness (sample was valid when generated)

**Trade-offs:**
- Potential for edge cases if smart contract strictly validates current depth
- May require contract changes to accept grace period submissions

#### 4. Dynamic Capacity Doubling

Allow nodes to temporarily adjust their capacity doubling factor based on network conditions:

```go
func (db *DB) AdaptiveCommittedDepth(churnRate float64) uint8 {
    baseDepth := db.reserveOptions.capacityDoubling + db.reserve.Radius()

    // During high churn, reduce committed depth to maintain participation
    if churnRate > highChurnThreshold {
        return baseDepth - 1
    }

    return baseDepth
}
```

**Benefits:**
- Maintains participation during unstable network periods
- Allows nodes to optimize for reward consistency vs. storage commitment

**Trade-offs:**
- Increases protocol complexity significantly
- May create gaming incentives if not carefully designed
- Requires contract changes to accept variable committed depths

#### 5. Improved Metrics and Observability

Add detailed metrics for depth change events:

```go
// Metrics to add:
- depth_change_total (counter with direction=increase/decrease)
- depth_stable_duration (histogram)
- rounds_skipped_due_to_depth_change (counter)
- sample_generation_during_depth_change (counter)
- current_depth_change_risk (gauge, 0.0-1.0)
```

**Benefits:**
- Better understanding of how often depth changes affect participation
- Data to inform tuning of dampening parameters
- Operator visibility into node behavior

#### 6. Peer Connection Strategy Optimization

Implement smarter peer connection strategies that consider round timing:

```go
func (k *Kad) ShouldConnectPeer(addr swarm.Address, proximity uint8) bool {
    // If near saturation and round is imminent, defer connection
    if k.depthChangeRisk() > 0.7 && k.timeUntilNextRound() < roundPreparationWindow {
        return false
    }
    return true
}
```

**Benefits:**
- Reduces depth changes during critical round preparation periods
- Improves participation reliability

**Trade-offs:**
- May slow network topology convergence
- Requires careful tuning to avoid harming network health

### Conclusion

The current round-skipping mechanism is **fundamentally sound** but could benefit from **dampening volatility** and **improving predictability**. The most impactful improvements would be:

1. **Depth change dampening** (recommendation #1) - Highest impact, moderate complexity
2. **Improved metrics** (recommendation #5) - High value, low complexity, should be implemented first
3. **Predictive notifications** (recommendation #2) - Medium impact, low complexity

The protocol correctly prioritizes consistency and correctness over participation rate, which is the right trade-off for a storage incentives system. However, reducing unnecessary volatility would improve the operator experience and overall network efficiency without compromising security.
