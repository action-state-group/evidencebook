package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/action-state-group/capsule-emit/go/artifact"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `deal node seal-model-call` seals one model call an agent made as an
// effect-boundary record of a node book (evidencebook#624): a Capsule signed
// in this process under the profile's key, whose effect is an inference
// completion the runtime claims (effect_attestation runtime_claimed, nothing
// stronger) and whose model_attestation names the provider and model id. The
// prompt is kept as the record's payload and committed by digest; the
// completion is committed by digest only and never stored.

func dealNodeSealModelCallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seal-model-call",
		Short: "Seal one model call as an effect-boundary record of a node book: the prompt kept, the completion by digest only (signs)",
		Long: `Seal one model call an agent made, as a record of the node book --book names, under --type. The
record is a Capsule signed in this process under the profile's key: its effect is an inference
completion, attested only as claimed by the runtime (runtime_claimed), and its model_attestation names
--provider and --model-id. --prompt and --completion are JSON value files. The prompt stays in this node's
store as the record's payload and is committed by digest; the completion is committed by digest and never
stored. --out writes the Capsule as a verifier reads it (with its signature and key_id) to a new file.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			book, _ := c.Flags().GetString("book")
			if err = nodeBookID(p, book); err != nil {
				return err
			}
			recordType, _ := c.Flags().GetString("type")
			provider, _ := c.Flags().GetString("provider")
			modelID, _ := c.Flags().GetString("model-id")
			if recordType == "" || provider == "" || modelID == "" {
				return cli.InputError("--type, --provider and --model-id are required: a model call names its provider and model id")
			}
			if libraryRecordTypes[recordType] {
				return cli.InputError("--type " + recordType + " is a record type the evidencebook library writes itself; a node's types are its own names")
			}
			prompt, err := readJSONValue(c, "prompt")
			if err != nil {
				return err
			}
			completion, err := readJSONValue(c, "completion")
			if err != nil {
				return err
			}
			developer, _ := c.Flags().GetString("developer")
			out, _ := c.Flags().GetString("out")
			var dest *newFile
			if out != "" {
				if dest, err = claimNewFile(out); err != nil {
					return err
				}
				defer dest.release()
			}
			ctx := c.Context()
			s, err := openDealSession(ctx, p)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, s.close()) }()
			b, err := s.shelf.Open(ctx, book)
			if err != nil {
				return err
			}
			requestDigest, err := emit.DigestJSON(prompt)
			if err != nil {
				return cli.InputError("--prompt: " + err.Error())
			}
			responseDigest, err := emit.DigestJSON(completion)
			if err != nil {
				return cli.InputError("--completion: " + err.Error())
			}
			idBytes := make([]byte, 8)
			if _, err = rand.Read(idBytes); err != nil {
				return err
			}
			actionID := "model-call-" + hex.EncodeToString(idBytes)
			at := cli.DealClock().UTC().Truncate(time.Second)
			identity, err := emit.NewEd25519SigningIdentity(s.key)
			if err != nil {
				return err
			}
			promptOctets, err := json.Marshal(prompt)
			if err != nil {
				return err
			}
			var sealed emit.Result
			seal := func(_ evidencebook.Header, header []byte) (emit.Result, error) {
				sealed, err = emit.Seal(emit.SealInput{
					Capsule: emit.Input{
						ActionID: actionID, ActionType: emit.ActionTypeFYI, Operator: p.Name, Developer: developer, Timestamp: at,
						Effect: &emit.Effect{
							Type: "inference_completion", Status: emit.EffectConfirmed, IrreversibilityClass: emit.IrreversibilityTwoWay,
							EffectAttestation: emit.AttestationRuntimeClaimed, RequestDigest: requestDigest, ResponseDigest: responseDigest,
						},
						Extensions: []emit.Extension{{Name: evidencebook.RecordHeaderMember, Value: header}},
					},
					Payload: prompt, AgentOutput: completion, Model: &emit.Model{Provider: provider, ModelID: modelID}, Identity: identity,
				})
				return sealed, err
			}
			s.inFlight = at
			defer func() { s.inFlight = time.Time{} }()
			record, err := b.AppendCapsule(ctx, evidencebook.Entry{RecordType: recordType, EpistemicType: evidencebook.ProducerClaim, SubjectRef: actionID},
				[]artifact.Artifact{{Name: "payload", Binding: artifact.PayloadDigest, State: artifact.Present, Content: promptOctets}}, seal)
			if err != nil {
				return err
			}
			capsule, err := capsuleAsVerified(sealed, s)
			if err != nil {
				return err
			}
			result := map[string]any{"record": nodeRecordOf(record), "capsule_id": sealed.CapsuleID}
			if dest != nil {
				if err := dest.commit(capsule); err != nil {
					return errors.Join(errors.New("the model call is sealed (record "+record.RecordID+"), but its capsule file was not saved"), err)
				}
				result["file"] = out
			} else {
				result["capsule"] = json.RawMessage(capsule)
			}
			return cli.Output(c, result)
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("type", "", "The node's own name for this record type (required)")
	cmd.Flags().String("provider", "", "The model's provider (required)")
	cmd.Flags().String("model-id", "", "The model's id (required)")
	cmd.Flags().String("prompt", "", "The prompt: a JSON value file, or - for standard input (required)")
	cmd.Flags().String("completion", "", "The completion: a JSON value file (required); committed by digest only, never stored")
	cmd.Flags().String("developer", "capsulectl-deal", "The developer name the Capsule carries")
	cmd.Flags().String("out", "", "Write the Capsule, with its signature and key_id, to this new file (default: in the result)")
	return cmd
}

// readJSONValue reads the JSON value file a flag names (- for standard input).
func readJSONValue(c *cobra.Command, flag string) (any, error) {
	path, _ := c.Flags().GetString(flag)
	if path == "" {
		return nil, cli.InputError("--" + flag + " is required: a JSON value file")
	}
	raw, err := readNodeInput(c, path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := decodeKeepingNumbers(raw, &v); err != nil {
		return nil, cli.InputError("--" + flag + " is not JSON: " + err.Error())
	}
	return v, nil
}

// capsuleAsVerified is the sealed Capsule as a verifier reads it: its JSON
// with the producer envelope's signature and the signing key_id added.
func capsuleAsVerified(sealed emit.Result, s *dealSession) ([]byte, error) {
	var capsule map[string]json.RawMessage
	if err := json.Unmarshal(sealed.Payload, &capsule); err != nil {
		return nil, err
	}
	signature, err := json.Marshal(hex.EncodeToString(sealed.Envelope))
	if err != nil {
		return nil, err
	}
	keyID, err := json.Marshal(hex.EncodeToString(s.key.Public().(ed25519.PublicKey)))
	if err != nil {
		return nil, err
	}
	capsule["signature"], capsule["key_id"] = signature, keyID
	return json.Marshal(capsule)
}

// newFile is a file the caller asked for, claimed before anything is sealed
// and filled by a hard link that fails if the path appeared since: never an
// existing file replaced, never a half-written one seen.
type newFile struct {
	path string
	tmp  *os.File
}

func claimNewFile(path string) (*newFile, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, cli.InputError("--out " + path + " exists: it is written to a new file, never over another")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".node-*")
	if err != nil {
		return nil, cli.InputError("--out cannot be written, and nothing was sealed: " + err.Error())
	}
	return &newFile{path: path, tmp: tmp}, nil
}

func (f *newFile) commit(data []byte) error {
	if _, err := f.tmp.Write(data); err != nil {
		return err
	}
	if err := f.tmp.Close(); err != nil {
		return err
	}
	return os.Link(f.tmp.Name(), f.path)
}

func (f *newFile) release() {
	_ = f.tmp.Close()
	_ = os.Remove(f.tmp.Name())
}
