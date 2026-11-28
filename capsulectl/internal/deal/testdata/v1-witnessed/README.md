# x-deal-v1 witnessed vectors

A real x-deal-v1 keep copy and the proof `deal checkpoint proof` wrote for it, from the ARD demo's
end-to-end run against the hosted witness on 2026-11-25. The data and keys are test data (deal
deal-fe219bb1ab3d4dec, Alder's side of a two-sided deal).

| File | What it is |
|---|---|
| `keep.json` | `capsulectl deal report --deal deal-fe219bb1ab3d4dec --bundle keep.json`: the holder's own copy, with the other party's half |
| `proof.json` | `capsulectl deal checkpoint proof --deal deal-fe219bb1ab3d4dec --copy keep.json --out proof.json`: the stamp proof and one receipt from the hosted witness |
| `witnesses.json` | The witness directory that names that witness, marked `"own_witness": true`: it is Action State's own (Steven's u655) |

`verify --bundle keep.json --stamp-proof proof.json --witness-directory witnesses.json` reports
`witnessed: {status: verified, witness: "Action State's witness", at: "2026-11-25T14:41:19Z",
independent_of: [], own_witness: true}` in the x-deal-v1 entry, with the verdict VALID. The receipt is
checked offline. With the mark, the receipt page draws no "does not list it as independent" sentence
for this witness (capsule-viewer#513); a row without it still does.
