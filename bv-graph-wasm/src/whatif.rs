//! What-If simulation for issue unblock cascade analysis.
//!
//! What-If analysis answers "If I close issue X, what happens?"
//! It computes direct unblocks, transitive cascades, and impact metrics.

use crate::graph::DiGraph;
use crate::reachability::{actionable_nodes, is_actionable};
use serde::Serialize;
use std::collections::VecDeque;

/// Result of a what-if simulation for closing a single node.
#[derive(Debug, Clone, Serialize)]
pub struct WhatIfResult {
    /// Number of issues directly unblocked (immediate dependents with all blockers satisfied)
    pub direct_unblocks: usize,
    /// Total issues transitively unblocked (full cascade)
    pub transitive_unblocks: usize,
    /// Indices of directly unblocked issues
    pub unblocked_ids: Vec<usize>,
    /// Indices of all transitively unblocked issues (includes direct)
    pub cascade_ids: Vec<usize>,
    /// Parallelization gain (new parallel opportunities created)
    pub parallel_gain: i32,
}

impl WhatIfResult {
    /// Create an empty result (no impact).
    pub fn empty() -> Self {
        WhatIfResult {
            direct_unblocks: 0,
            transitive_unblocks: 0,
            unblocked_ids: Vec::new(),
            cascade_ids: Vec::new(),
            parallel_gain: 0,
        }
    }
}

/// Compute what happens if a node is "closed" (removed from blocking consideration).
///
/// # Arguments
/// * `graph` - The dependency graph
/// * `node` - The node to simulate closing
/// * `closed_set` - Boolean array indicating which nodes are already closed
///
/// # Returns
/// WhatIfResult with direct unblocks, transitive cascade, and impact metrics.
pub fn what_if_close(graph: &DiGraph, node: usize, closed_set: &[bool]) -> WhatIfResult {
    let n = graph.len();
    if node >= n || closed_set.get(node).copied().unwrap_or(false) {
        // Node doesn't exist or is already closed
        return WhatIfResult::empty();
    }

    // Create new closed set with this node added
    let mut new_closed = closed_set.to_vec();
    new_closed.resize(n, false);
    new_closed[node] = true;

    // Find issues that become actionable (directly unblocked)
    // Incoming edges identify dependents that had all other prerequisites closed.
    let mut direct_unblocks = Vec::new();

    for &dependent in graph.predecessors_slice(node) {
        if new_closed[dependent] {
            continue;
        }

        // Was this dependent blocked before?
        let was_blocked = !is_actionable(graph, dependent, closed_set);

        // Is it unblocked now?
        let now_unblocked = is_actionable(graph, dependent, &new_closed);

        if was_blocked && now_unblocked {
            direct_unblocks.push(dependent);
        }
    }

    // Count transitive unblocks (cascade effect)
    // BFS from direct unblocks, adding nodes as they become actionable
    let cascade_ids = count_cascade(graph, &direct_unblocks, &new_closed);

    let transitive_count = cascade_ids.len();
    let direct_count = direct_unblocks.len();

    WhatIfResult {
        direct_unblocks: direct_count,
        transitive_unblocks: transitive_count,
        unblocked_ids: direct_unblocks,
        cascade_ids,
        parallel_gain: direct_count.saturating_sub(1) as i32,
    }
}

/// Count the cascade of nodes that become actionable starting from roots.
///
/// Uses BFS simulation where we "close" each unblocked node and check
/// what else becomes actionable.
fn count_cascade(graph: &DiGraph, roots: &[usize], initial_closed: &[bool]) -> Vec<usize> {
    let n = graph.len();
    if n == 0 || roots.is_empty() {
        return roots.to_vec();
    }

    let mut closed = initial_closed.to_vec();
    closed.resize(n, false);

    let mut visited = vec![false; n];
    let mut cascade = Vec::new();
    let mut queue: VecDeque<usize> = VecDeque::new();

    // Initialize with roots
    for &root in roots {
        if root < n && !visited[root] && !closed[root] {
            visited[root] = true;
            cascade.push(root);
            queue.push_back(root);
        }
    }

    // BFS: simulate completing each node and check what unblocks
    while let Some(v) = queue.pop_front() {
        // Mark this node as "completed" for cascade purposes
        closed[v] = true;

        // Check dependents (incoming neighbors).
        for &w in graph.predecessors_slice(v) {
            if visited[w] || closed[w] {
                continue;
            }

            // Check if all prerequisites of w are now resolved.
            let all_resolved = graph
                .successors_slice(w)
                .iter()
                .all(|&p| closed[p] || visited[p]);

            if all_resolved {
                visited[w] = true;
                cascade.push(w);
                queue.push_back(w);
            }
        }
    }

    cascade
}

/// Result entry for top what-if ranking.
#[derive(Debug, Clone, Serialize)]
pub struct TopWhatIfEntry {
    /// Node index
    pub node: usize,
    /// What-if result for this node
    pub result: WhatIfResult,
}

/// Find top N issues with highest cascade impact.
///
/// # Arguments
/// * `graph` - The dependency graph
/// * `closed_set` - Boolean array indicating which nodes are already closed
/// * `limit` - Maximum number of results to return
/// * `candidate_set` - Optional direct-selection eligibility mask. Excluded
///   nodes still participate in dependency checks; missing entries are false.
///
/// # Returns
/// Vector of (node, WhatIfResult) sorted by transitive_unblocks descending.
pub fn top_what_if(
    graph: &DiGraph,
    closed_set: &[bool],
    limit: usize,
    candidate_set: Option<&[u8]>,
) -> Vec<TopWhatIfEntry> {
    let n = graph.len();
    if n == 0 {
        return Vec::new();
    }

    // Get currently actionable nodes (candidates for closing)
    let candidates = actionable_nodes(graph, closed_set);

    let mut results: Vec<TopWhatIfEntry> = candidates
        .into_iter()
        .filter(|&node| candidate_set.is_none_or(|set| set.get(node).is_some_and(|&b| b != 0)))
        .map(|node| {
            let result = what_if_close(graph, node, closed_set);
            TopWhatIfEntry { node, result }
        })
        .filter(|e| e.result.transitive_unblocks > 0)
        .collect();

    // Sort by transitive impact (descending), then direct, then node ID for determinism
    results.sort_by(|a, b| {
        b.result
            .transitive_unblocks
            .cmp(&a.result.transitive_unblocks)
            .then_with(|| b.result.direct_unblocks.cmp(&a.result.direct_unblocks))
            .then_with(|| a.node.cmp(&b.node))
    });

    results.truncate(limit);
    results
}

/// Find all issues with any unblock potential.
///
/// Similar to top_what_if but returns all issues (not just actionable ones)
/// sorted by their cascade impact. Useful for identifying high-impact blocked items.
pub fn all_what_if(graph: &DiGraph, closed_set: &[bool], limit: usize) -> Vec<TopWhatIfEntry> {
    let n = graph.len();
    if n == 0 {
        return Vec::new();
    }

    let mut closed = closed_set.to_vec();
    closed.resize(n, false);

    let mut results: Vec<TopWhatIfEntry> = (0..n)
        .filter(|&i| !closed[i])
        .map(|node| {
            let result = what_if_close(graph, node, &closed);
            TopWhatIfEntry { node, result }
        })
        .filter(|e| e.result.transitive_unblocks > 0)
        .collect();

    results.sort_by(|a, b| {
        b.result
            .transitive_unblocks
            .cmp(&a.result.transitive_unblocks)
            .then_with(|| b.result.direct_unblocks.cmp(&a.result.direct_unblocks))
            .then_with(|| a.node.cmp(&b.node))
    });

    results.truncate(limit);
    results
}

/// Batch what-if: compute impact of closing multiple nodes at once.
///
/// # Arguments
/// * `graph` - The dependency graph
/// * `nodes` - Nodes to simulate closing together
/// * `closed_set` - Boolean array indicating which nodes are already closed
///
/// # Returns
/// Combined WhatIfResult for closing all specified nodes.
pub fn what_if_close_batch(graph: &DiGraph, nodes: &[usize], closed_set: &[bool]) -> WhatIfResult {
    let n = graph.len();
    if n == 0 || nodes.is_empty() {
        return WhatIfResult::empty();
    }

    // Create closed set with all specified nodes added
    let mut new_closed = closed_set.to_vec();
    new_closed.resize(n, false);
    for &node in nodes {
        if node < n {
            new_closed[node] = true;
        }
    }

    // Find all issues that become directly actionable
    let mut direct_unblocks = Vec::new();
    let mut seen = vec![false; n];

    for &node in nodes {
        if node >= n {
            continue;
        }
        for &dependent in graph.predecessors_slice(node) {
            if seen[dependent] || new_closed[dependent] {
                continue;
            }
            seen[dependent] = true;

            let was_blocked = !is_actionable(graph, dependent, closed_set);
            let now_unblocked = is_actionable(graph, dependent, &new_closed);

            if was_blocked && now_unblocked {
                direct_unblocks.push(dependent);
            }
        }
    }

    let cascade_ids = count_cascade(graph, &direct_unblocks, &new_closed);
    let transitive_count = cascade_ids.len();
    let direct_count = direct_unblocks.len();

    WhatIfResult {
        direct_unblocks: direct_count,
        transitive_unblocks: transitive_count,
        unblocked_ids: direct_unblocks,
        cascade_ids,
        parallel_gain: direct_count.saturating_sub(1) as i32,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_what_if_empty() {
        let graph = DiGraph::new();
        let result = what_if_close(&graph, 0, &[]);
        assert_eq!(result.direct_unblocks, 0);
        assert_eq!(result.transitive_unblocks, 0);
    }

    #[test]
    fn test_what_if_single_node() {
        let mut graph = DiGraph::new();
        graph.add_node("a");
        let result = what_if_close(&graph, 0, &[false]);
        assert_eq!(result.direct_unblocks, 0);
        assert_eq!(result.transitive_unblocks, 0);
    }

    #[test]
    fn test_what_if_simple_chain() {
        // a <- b <- c: each dependent points to its prerequisite.
        // Closing a should unblock b, then c transitively
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        graph.add_edge(b, a);
        graph.add_edge(c, b);

        let closed = vec![false, false, false];
        let result = what_if_close(&graph, a, &closed);

        assert_eq!(result.direct_unblocks, 1); // b
        assert_eq!(result.transitive_unblocks, 2); // b and c
        assert!(result.unblocked_ids.contains(&b));
        assert!(result.cascade_ids.contains(&b));
        assert!(result.cascade_ids.contains(&c));

        let leaf_result = what_if_close(&graph, c, &closed);
        assert_eq!(leaf_result.direct_unblocks, 0);
        assert_eq!(leaf_result.transitive_unblocks, 0);
        assert!(leaf_result.unblocked_ids.is_empty());
        assert!(leaf_result.cascade_ids.is_empty());
    }

    #[test]
    fn test_what_if_diamond() {
        // b -> a, c -> a; d depends on both b and c.
        // Closing a should unblock b and c directly, then d transitively
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        let d = graph.add_node("d");
        graph.add_edge(b, a);
        graph.add_edge(c, a);
        graph.add_edge(d, b);
        graph.add_edge(d, c);

        let closed = vec![false, false, false, false];
        let result = what_if_close(&graph, a, &closed);

        assert_eq!(result.direct_unblocks, 2); // b and c
        assert_eq!(result.transitive_unblocks, 3); // b, c, and d
        assert_eq!(result.parallel_gain, 1); // 2 - 1 = 1
    }

    #[test]
    fn test_what_if_partial_close() {
        // c -> a, c -> b
        // If a is already closed, closing b unblocks c
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        graph.add_edge(c, a);
        graph.add_edge(c, b);

        // a is closed
        let closed = vec![true, false, false];
        let result = what_if_close(&graph, b, &closed);

        assert_eq!(result.direct_unblocks, 1); // c
        assert_eq!(result.transitive_unblocks, 1); // just c
    }

    #[test]
    fn test_what_if_multi_blocker_not_ready() {
        // c -> a, c -> b
        // Neither closed: closing a doesn't unblock c (b still blocks it)
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        graph.add_edge(c, a);
        graph.add_edge(c, b);

        let closed = vec![false, false, false];
        let result = what_if_close(&graph, a, &closed);

        assert_eq!(result.direct_unblocks, 0); // c still blocked by b
        assert_eq!(result.transitive_unblocks, 0);
    }

    #[test]
    fn test_what_if_already_closed() {
        let mut graph = DiGraph::new();
        graph.add_node("a");
        graph.add_node("b");
        graph.add_edge(1, 0);

        let closed = vec![true, false];
        let result = what_if_close(&graph, 0, &closed);

        assert_eq!(result.direct_unblocks, 0);
        assert_eq!(result.transitive_unblocks, 0);

        // Closing an open prerequisite does not count its closed dependent.
        let result = what_if_close(&graph, 0, &[false, true]);
        assert_eq!(result.direct_unblocks, 0);
        assert_eq!(result.transitive_unblocks, 0);
        assert!(result.cascade_ids.is_empty());
    }

    #[test]
    fn test_what_if_wide_fanout() {
        // b1, b2, b3, b4, b5 each point to prerequisite a.
        // Closing a unblocks all 5
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        for i in 0..5 {
            let b = graph.add_node(&format!("b{}", i));
            graph.add_edge(b, a);
        }

        let closed = vec![false; 6];
        let result = what_if_close(&graph, a, &closed);

        assert_eq!(result.direct_unblocks, 5);
        assert_eq!(result.transitive_unblocks, 5);
        assert_eq!(result.parallel_gain, 4); // 5 - 1
    }

    #[test]
    fn test_what_if_deep_cascade() {
        // a <- b <- c <- d <- e <- f (chain of 6)
        let mut graph = DiGraph::new();
        let mut prev = graph.add_node("a");
        for i in 1..6 {
            let node = graph.add_node(&format!("n{}", i));
            graph.add_edge(node, prev);
            prev = node;
        }

        let closed = vec![false; 6];
        let result = what_if_close(&graph, 0, &closed);

        assert_eq!(result.direct_unblocks, 1);
        assert_eq!(result.transitive_unblocks, 5); // All 5 subsequent nodes
    }

    #[test]
    fn test_top_what_if() {
        // b, c, d depend on a; f depends on e.
        // a has more impact than e
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        let d = graph.add_node("d");
        let e = graph.add_node("e");
        let f = graph.add_node("f");
        graph.add_edge(b, a);
        graph.add_edge(c, a);
        graph.add_edge(d, a);
        graph.add_edge(f, e);

        let closed = vec![false; 6];
        let top = top_what_if(&graph, &closed, 10, None);

        assert_eq!(top.len(), 2);
        // a should be first (unblocks 3)
        assert_eq!(top[0].node, a);
        assert_eq!(top[0].result.transitive_unblocks, 3);

        // e should be second (unblocks 1)
        assert_eq!(top[1].node, e);
        assert_eq!(top[1].result.transitive_unblocks, 1);
    }

    #[test]
    fn test_top_what_if_limit() {
        let mut graph = DiGraph::new();
        for i in 0..10 {
            let a = graph.add_node(&format!("a{}", i));
            let b = graph.add_node(&format!("b{}", i));
            graph.add_edge(b, a);
        }

        let closed = vec![false; 20];
        let top = top_what_if(&graph, &closed, 3, None);

        assert_eq!(top.len(), 3);
    }

    #[test]
    fn top_what_if_candidate_mask_preserves_unresolved_context() {
        let mut graph = DiGraph::new();
        for i in 0..7 {
            graph.add_node(&format!("n{i}"));
        }
        for (from, to) in [(1, 0), (2, 6), (3, 6), (4, 6), (5, 0), (5, 6)] {
            graph.add_edge(from, to);
        }
        assert_eq!(top_what_if(&graph, &[], 1, None)[0].node, 6);
        for mask in [&[1][..], &[1, 1, 1, 1, 1, 1, 0][..]] {
            let top = top_what_if(&graph, &[], 1, Some(mask));
            assert_eq!(top.len(), 1);
            assert_eq!(top[0].node, 0);
            assert_eq!(top[0].result.cascade_ids, vec![1]);
            assert_eq!(top[0].result.transitive_unblocks, 1);
        }
        for mask in [&[][..], &[0; 7][..]] {
            assert!(top_what_if(&graph, &[], 5, Some(mask)).is_empty());
        }
        assert!(top_what_if(&graph, &[], 0, Some(&[1; 7])).is_empty());
        assert!(top_what_if(&graph, &[true], 5, Some(&[1])).is_empty());
        assert_eq!(
            serde_json::to_value(top_what_if(&graph, &[], 5, None)).unwrap(),
            serde_json::to_value(top_what_if(&graph, &[], 5, Some(&[2; 9]))).unwrap()
        );
    }

    #[test]
    fn test_what_if_batch_simple() {
        // c -> a, c -> b
        // Closing both a and b should unblock c
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        graph.add_edge(c, a);
        graph.add_edge(c, b);

        let closed = vec![false, false, false];
        let result = what_if_close_batch(&graph, &[a, b], &closed);

        assert_eq!(result.direct_unblocks, 1); // c
        assert_eq!(result.transitive_unblocks, 1);
    }

    #[test]
    fn test_what_if_batch_cascade() {
        // c -> a, d -> b; e depends on both c and d.
        // Closing both a and b unblocks c, d, then e
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        let d = graph.add_node("d");
        let e = graph.add_node("e");
        graph.add_edge(c, a);
        graph.add_edge(d, b);
        graph.add_edge(e, c);
        graph.add_edge(e, d);

        let closed = vec![false; 5];
        let result = what_if_close_batch(&graph, &[a, b], &closed);

        assert_eq!(result.direct_unblocks, 2); // c and d
        assert_eq!(result.transitive_unblocks, 3); // c, d, e
    }

    #[test]
    fn test_all_what_if() {
        // b -> a, c (isolated)
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let _c = graph.add_node("c");
        graph.add_edge(b, a);

        let closed = vec![false, false, false];
        let all = all_what_if(&graph, &closed, 10);

        // Only a has impact (c is isolated, b is blocked)
        assert_eq!(all.len(), 1);
        assert_eq!(all[0].node, a);
    }

    #[test]
    fn test_what_if_cycle_handling() {
        // a -> b -> c -> a (cycle)
        // Closing a releases its direct dependent c, then b in the cascade.
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        graph.add_edge(a, b);
        graph.add_edge(b, c);
        graph.add_edge(c, a);

        let closed = vec![false, false, false];

        // No candidate is initially actionable, but explicitly closing a
        // breaks this cycle and allows the remaining work to cascade.
        assert!(top_what_if(&graph, &closed, 10, None).is_empty());
        let result = what_if_close(&graph, a, &closed);
        assert_eq!(result.direct_unblocks, 1);
        assert_eq!(result.unblocked_ids, vec![c]);
        assert_eq!(result.transitive_unblocks, 2);
        assert_eq!(result.cascade_ids, vec![c, b]);
    }

    #[test]
    fn test_what_if_disconnected_components() {
        // Component 1: b -> a
        // Component 2: d -> c
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        let d = graph.add_node("d");
        graph.add_edge(b, a);
        graph.add_edge(d, c);

        let closed = vec![false; 4];

        // Closing a should only affect component 1
        let result_a = what_if_close(&graph, a, &closed);
        assert_eq!(result_a.transitive_unblocks, 1);
        assert!(result_a.cascade_ids.contains(&b));

        // Closing c should only affect component 2
        let result_c = what_if_close(&graph, c, &closed);
        assert_eq!(result_c.transitive_unblocks, 1);
        assert!(result_c.cascade_ids.contains(&d));
    }

    #[test]
    fn test_cascade_order() {
        // a <- b <- c <- d (deep chain)
        let mut graph = DiGraph::new();
        let a = graph.add_node("a");
        let b = graph.add_node("b");
        let c = graph.add_node("c");
        let d = graph.add_node("d");
        graph.add_edge(b, a);
        graph.add_edge(c, b);
        graph.add_edge(d, c);

        let closed = vec![false; 4];
        let result = what_if_close(&graph, a, &closed);

        // Cascade should include b, c, d in order
        assert_eq!(result.cascade_ids.len(), 3);
        assert_eq!(result.cascade_ids[0], b);
        assert_eq!(result.cascade_ids[1], c);
        assert_eq!(result.cascade_ids[2], d);
    }
}
