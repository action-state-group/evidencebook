//! The cadence tree (Go `cadence_tree.go`, from capsule-cli's
//! `deal_cadence.go` at `7a6b101`, byte for byte): each tick commits every
//! live log's checkpoint as a salted leaf at a random position of a sparse,
//! fixed-depth tree, and only the root goes to the cadence log and its
//! witness. `testdata/shelf/cadence` holds vectors written by capsule-cli's
//! code and `testdata/shelf/tree.json` vectors written by Go's.

use super::random::Draws;
use super::ShelfError;
use crate::canonical;
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::OnceLock;

/// The tree's fixed depth: every path is 16 hashes, and a tick holds at
/// most 2^16 - 1 logs beside its filler.
pub const CADENCE_DEPTH: usize = 16;

const CAPACITY: u64 = 1 << CADENCE_DEPTH;

/// `SHA-256(0x00 ‖ JCS({checkpoint_sha256, log_id, salt, size}))`, `size`
/// being the log's MMR size at the checkpoint.
pub fn cadence_leaf(log_id: &str, size: u64, checkpoint_sha256: &str, salt: &str) -> [u8; 32] {
    let jcs = canonical::jcs(&json!({
        "checkpoint_sha256": checkpoint_sha256, "log_id": log_id, "salt": salt, "size": size,
    }))
    .expect("strings and an integer canonicalize");
    let mut h = Sha256::new();
    h.update([0x00]);
    h.update(jcs);
    h.finalize().into()
}

/// `SHA-256(0x03 ‖ nonce)`.
pub fn cadence_filler_leaf(nonce: &[u8]) -> [u8; 32] {
    let mut h = Sha256::new();
    h.update([0x03]);
    h.update(nonce);
    h.finalize().into()
}

fn node(left: &[u8; 32], right: &[u8; 32]) -> [u8; 32] {
    let mut h = Sha256::new();
    h.update([0x01]);
    h.update(left);
    h.update(right);
    h.finalize().into()
}

/// `empty()[h]` is the root of an empty subtree of height `h`, from the
/// empty leaf `SHA-256(0x02)`.
pub fn cadence_empty() -> &'static [[u8; 32]; CADENCE_DEPTH + 1] {
    static EMPTY: OnceLock<[[u8; 32]; CADENCE_DEPTH + 1]> = OnceLock::new();
    EMPTY.get_or_init(|| {
        let mut levels = [[0u8; 32]; CADENCE_DEPTH + 1];
        levels[0] = Sha256::digest([0x02]).into();
        for h in 1..=CADENCE_DEPTH {
            levels[h] = node(&levels[h - 1], &levels[h - 1]);
        }
        levels
    })
}

/// The root of the sparse tree over `leaves` and, for `index`, its path:
/// the sibling at each height, leaf level first. A missing sibling is the
/// empty subtree's root.
pub fn cadence_tree(leaves: &HashMap<u64, [u8; 32]>, index: u64) -> ([u8; 32], Vec<[u8; 32]>) {
    let empty = cadence_empty();
    let mut level = leaves.clone();
    let mut index = index;
    let mut path = Vec::with_capacity(CADENCE_DEPTH);
    for empty_h in &empty[..CADENCE_DEPTH] {
        path.push(*level.get(&(index ^ 1)).unwrap_or(empty_h));
        let mut next = HashMap::with_capacity(level.len() / 2 + 1);
        for &i in level.keys() {
            let parent = i >> 1;
            if next.contains_key(&parent) {
                continue;
            }
            let left = level.get(&(parent << 1)).unwrap_or(empty_h);
            let right = level.get(&(parent << 1 | 1)).unwrap_or(empty_h);
            next.insert(parent, node(left, right));
        }
        level = next;
        index >>= 1;
    }
    (*level.get(&0).unwrap_or(&empty[CADENCE_DEPTH]), path)
}

/// Recompute a root from a leaf, its position and its path.
pub fn cadence_fold(leaf: &[u8; 32], index: u64, path: &[[u8; 32]]) -> [u8; 32] {
    let mut h = *leaf;
    for (level, sibling) in path.iter().enumerate() {
        h = if (index >> level) & 1 == 0 {
            node(&h, sibling)
        } else {
            node(sibling, &h)
        };
    }
    h
}

/// One log's leaf in one tick, as stored on the device (capsule-cli's
/// `dealTickLeaf`, member for member).
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct TickLeaf {
    pub log_id: String,
    pub size: u64,
    pub checkpoint_sha256: String,
    pub salt: String,
    pub index: u64,
    /// The log's checkpoint itself (base64url COSE), kept so a later bundle
    /// can show an earlier witnessed checkpoint is a prefix of its current
    /// one. The leaf commits to its SHA-256.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub statement: String,
}

/// Every leaf of one tick (capsule-cli's `dealTickLeaves` plus the derived
/// fillers that keep a tick's occupancy at the lifetime log count).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TickLeaves {
    pub deals: BTreeMap<String, TickLeaf>,
    pub filler_index: u64,
    pub filler_nonce: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub filler_seed: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub filler_count: u64,
}

fn is_zero(n: &u64) -> bool {
    *n == 0
}

impl TickLeaves {
    /// The leaves as `cadence_ticks.leaves` stores them (Go's
    /// `json.Marshal`).
    pub fn to_json(&self) -> String {
        super::gojson::marshal(&serde_json::to_value(self).expect("tick leaves serialize"))
    }

    /// Rebuild the tick's tree from its stored leaves; the root and the
    /// path of the leaf at `index`.
    pub fn tree(&self, index: u64) -> Result<([u8; 32], Vec<[u8; 32]>), ShelfError> {
        // A position past the tree would fold into another one, and its
        // leaf would silently leave the root.
        if index >= CAPACITY || self.filler_index >= CAPACITY {
            return Err(ShelfError::Corrupt(format!(
                "a cadence position is under {CAPACITY}"
            )));
        }
        let mut leaves = HashMap::new();
        let mut used = HashSet::new();
        let nonce = hex::decode(&self.filler_nonce)
            .map_err(|e| ShelfError::Corrupt(format!("filler nonce: {e}")))?;
        leaves.insert(self.filler_index, cadence_filler_leaf(&nonce));
        used.insert(self.filler_index);
        for leaf in self.deals.values() {
            if leaf.index >= CAPACITY {
                return Err(ShelfError::Corrupt(format!(
                    "position {} of {} is past the tree",
                    leaf.index, leaf.log_id
                )));
            }
            if !used.insert(leaf.index) {
                return Err(ShelfError::Corrupt(format!(
                    "two leaves at position {}",
                    leaf.index
                )));
            }
            leaves.insert(
                leaf.index,
                cadence_leaf(&leaf.log_id, leaf.size, &leaf.checkpoint_sha256, &leaf.salt),
            );
        }
        if self.filler_count > 0 {
            let seed: [u8; 32] = hex::decode(&self.filler_seed)
                .ok()
                .and_then(|s| s.try_into().ok())
                .ok_or_else(|| ShelfError::Corrupt("filler seed".into()))?;
            leaves.extend(derived_fillers(&seed, self.filler_count, &mut used)?);
        }
        Ok(cadence_tree(&leaves, index))
    }
}

/// Fillers 1..=`count` of a tick, by position (Go `DerivedFillers`). Filler
/// `i` is `SHA-256(0x03 ‖ n_i)` with `n_i = SHA-256(seed ‖ uint64be(i))`;
/// its position is the first unused one among, for attempt a = 0, 1, …,
/// `uint64be(SHA-256(seed ‖ "pos" ‖ uint64be(i) ‖ uint32be(a))[0:8]) mod
/// 2^16`, after the logs' leaves and filler 0 (`used`) and the fillers
/// before it. `used` is updated.
pub fn derived_fillers(
    seed: &[u8],
    count: u64,
    used: &mut HashSet<u64>,
) -> Result<HashMap<u64, [u8; 32]>, ShelfError> {
    let taken = used.len() as u64;
    if taken > CAPACITY || count > CAPACITY - taken {
        return Err(ShelfError::Invalid(format!(
            "{count} fillers do not fit beside {taken} leaves"
        )));
    }
    let mut out = HashMap::with_capacity(count as usize);
    for i in 1..=count {
        let ib = i.to_be_bytes();
        let mut h = Sha256::new();
        h.update(seed);
        h.update(ib);
        let nonce: [u8; 32] = h.finalize().into();
        for a in 0u32.. {
            let mut h = Sha256::new();
            h.update(seed);
            h.update(b"pos");
            h.update(ib);
            h.update(a.to_be_bytes());
            let digest = h.finalize();
            let position =
                u64::from_be_bytes(digest[..8].try_into().expect("eight bytes")) % CAPACITY;
            if used.insert(position) {
                out.insert(position, cadence_filler_leaf(&nonce));
                break;
            }
        }
    }
    Ok(out)
}

/// A fresh, uniform, unused tree position (Go `positions.random`).
pub(crate) fn random_position(
    used: &mut HashSet<u64>,
    draws: &mut Draws,
) -> Result<u64, ShelfError> {
    if used.len() as u64 >= CAPACITY {
        return Err(ShelfError::Invalid("the cadence tree is full".into()));
    }
    loop {
        let i = draws.uniform(CAPACITY)?;
        if used.insert(i) {
            return Ok(i);
        }
    }
}

/// The id of an Evidence Layer padding record for `nonce` (64 lowercase
/// hex): the JCS digest of `{"record_type": "padding", "epistemic_type":
/// "producer_claim", "store_nonce": nonce}` (capsule-cli's
/// `dealPaddingRecordID`).
pub fn padding_record_id(nonce: &str) -> String {
    canonical::json_digest(&json!({
        "record_type": "padding", "epistemic_type": "producer_claim", "store_nonce": nonce,
    }))
    .expect("strings canonicalize")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::shelf::random::SeededStream;
    use serde_json::Value;

    fn vectors() -> Value {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../testdata/shelf/tree.json"
        );
        serde_json::from_slice(&std::fs::read(path).expect("testdata/shelf/tree.json")).unwrap()
    }

    fn h(v: &Value) -> String {
        v.as_str().unwrap().to_string()
    }

    #[test]
    fn reads_the_tree_vectors_byte_for_byte() {
        let v = vectors();
        assert_eq!(v["depth"], CADENCE_DEPTH);
        let empty: Vec<String> = cadence_empty().iter().map(hex::encode).collect();
        let want: Vec<String> = v["empty_roots"].as_array().unwrap().iter().map(h).collect();
        assert_eq!(empty, want);
        for leaf in v["leaves"].as_array().unwrap() {
            let got = cadence_leaf(
                leaf["log_id"].as_str().unwrap(),
                leaf["size"].as_u64().unwrap(),
                leaf["checkpoint_sha256"].as_str().unwrap(),
                leaf["salt"].as_str().unwrap(),
            );
            assert_eq!(hex::encode(got), h(&leaf["leaf"]));
        }
        for filler in v["fillers"].as_array().unwrap() {
            let nonce = hex::decode(h(&filler["nonce"])).unwrap();
            assert_eq!(hex::encode(cadence_filler_leaf(&nonce)), h(&filler["leaf"]));
        }
        for d in v["derived_fillers"].as_array().unwrap() {
            let seed = hex::decode(h(&d["seed"])).unwrap();
            let mut used: HashSet<u64> = d["used"]
                .as_array()
                .unwrap()
                .iter()
                .map(|u| u.as_u64().unwrap())
                .collect();
            let got = derived_fillers(&seed, d["count"].as_u64().unwrap(), &mut used).unwrap();
            let want = d["fillers"].as_array().unwrap();
            assert_eq!(got.len(), want.len());
            for f in want {
                let at = f["position"].as_u64().unwrap();
                assert_eq!(hex::encode(got[&at]), h(&f["leaf"]));
            }
        }
        for t in v["trees"].as_array().unwrap() {
            let stored = t["leaves"].as_str().unwrap();
            let leaves: TickLeaves = serde_json::from_str(stored).unwrap();
            assert_eq!(
                leaves.to_json(),
                stored,
                "a tick's leaves are stored as Go stores them"
            );
            for p in t["paths"].as_array().unwrap() {
                let index = p["index"].as_u64().unwrap();
                let (root, path) = leaves.tree(index).unwrap();
                assert_eq!(hex::encode(root), h(&t["root"]));
                let want: Vec<String> = p["path"].as_array().unwrap().iter().map(h).collect();
                let got: Vec<String> = path.iter().map(hex::encode).collect();
                assert_eq!(got, want);
            }
        }
        for p in v["padding"].as_array().unwrap() {
            assert_eq!(padding_record_id(&h(&p["nonce"])), h(&p["record_id"]));
        }
        let mut draws = Draws(Box::new(SeededStream::new(
            v["draw_stream"].as_str().unwrap(),
        )));
        for d in v["draws"].as_array().unwrap() {
            match d["draw"].as_str().unwrap() {
                "bytes" => {
                    let n = d["n"].as_u64().unwrap() as usize;
                    assert_eq!(draws.hex(n).unwrap(), h(&d["value"]));
                }
                "uniform" => {
                    assert_eq!(
                        draws.uniform(d["n"].as_u64().unwrap()).unwrap(),
                        d["value"].as_u64().unwrap()
                    );
                }
                "position" => {
                    let mut used: HashSet<u64> = d
                        .get("used")
                        .and_then(Value::as_array)
                        .into_iter()
                        .flatten()
                        .map(|u| u.as_u64().unwrap())
                        .collect();
                    assert_eq!(
                        random_position(&mut used, &mut draws).unwrap(),
                        d["value"].as_u64().unwrap()
                    );
                }
                other => panic!("unknown draw {other}"),
            }
        }
    }

    #[test]
    fn a_path_folds_back_to_its_root_and_a_position_past_the_tree_is_refused() {
        let mut leaves = TickLeaves {
            filler_index: 5,
            filler_nonce: "00".repeat(32),
            ..Default::default()
        };
        leaves.deals.insert(
            "a".into(),
            TickLeaf {
                log_id: "a".into(),
                size: 1,
                checkpoint_sha256: "11".repeat(32),
                salt: "22".repeat(32),
                index: 9,
                statement: String::new(),
            },
        );
        let (root, path) = leaves.tree(9).unwrap();
        let leaf = cadence_leaf("a", 1, &"11".repeat(32), &"22".repeat(32));
        assert_eq!(cadence_fold(&leaf, 9, &path), root);
        assert!(leaves.tree(CAPACITY).is_err());
        leaves.deals.get_mut("a").unwrap().index = 5;
        assert!(leaves.tree(5).is_err(), "two leaves at one position");
    }
}
