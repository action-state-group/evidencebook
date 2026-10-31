package book

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// The holder's pages are Steven's 9b frames 2 (What I share), 3 (They want
// more) and 9 (Settings to prompt), drawn from what the book holds. A row the
// book has no producer for (a window, an attach switch, an ask-me setting, a
// rate limit) shows "Not available yet" and nothing else: no default is drawn
// as if it were in force, and the canvas's example values (Dana R., 12 against
// 3) are never data. All words come from a wording pack; the canvas strings
// are the embedded pack's.
//
//go:embed assets/holder-pages/wording-en.json
var holderWordingJSON []byte

//go:embed assets/holder-pages/holder_pages.css
var holderPagesCSS string

const (
	viewShare  = "share"
	viewAsked  = "asked"
	viewPrompt = "prompt"
)

// holderWording is an aac.wording-pack/v0 pack as the holder pages read it.
type holderWording struct {
	entries map[string]string
	shared  bool
}

// The shared form of a string is its ".shared" entry, when the pack has one:
// frame 2 only (a shared asks page would tell a third party who asked about
// the holder).
func (w holderWording) get(key string) string {
	if w.shared {
		if v, ok := w.entries[key+".shared"]; ok {
			return v
		}
	}
	return w.entries[key]
}

// text escapes a pack string, except <b> and </b>, the only markup a pack may
// carry.
func (w holderWording) text(key string, args ...string) template.HTML {
	s := template.HTMLEscapeString(w.get(key))
	s = strings.ReplaceAll(strings.ReplaceAll(s, "&lt;b&gt;", "<b>"), "&lt;/b&gt;", "</b>")
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", template.HTMLEscapeString(args[i+1]))
	}
	return template.HTML(s) //nolint:gosec // the pack and every value are escaped; only the pack's <b> is markup
}

func (w holderWording) plain(key string, args ...string) string {
	s := w.get(key)
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}

// decodeHolderWording reads a pack and refuses one that lacks an entry the
// pages draw, naming it, so a page never renders a blank.
func decodeHolderWording(raw []byte, shared bool) (map[string]string, error) {
	var pack struct {
		Version string            `json:"wording_pack_version"`
		ID      string            `json:"id"`
		Locale  string            `json:"locale"`
		Entries map[string]string `json:"entries"`
	}
	if err := json.Unmarshal(raw, &pack); err != nil {
		return nil, fmt.Errorf("the wording pack is not JSON: %w", err)
	}
	if pack.Version != "aac.wording-pack/v0" || pack.ID == "" || pack.Locale != "en" {
		return nil, errors.New("the wording pack is not an aac.wording-pack/v0 pack with an id and locale en")
	}
	var base struct {
		Entries map[string]string `json:"entries"`
	}
	if err := json.Unmarshal(holderWordingJSON, &base); err != nil {
		return nil, err
	}
	var missing []string
	for key := range base.Entries {
		if strings.HasSuffix(key, ".shared") && !shared {
			continue
		}
		if value, ok := pack.Entries[key]; !ok || value == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("the wording pack has no (or an empty) entry for %s", strings.Join(missing, ", "))
	}
	return pack.Entries, nil
}

type pageRow struct {
	Label, Pill, Class, Note template.HTML
}

type pageSection struct {
	Title template.HTML
	Rows  []pageRow
	Note  template.HTML
	Never bool
}

type askedItem struct {
	Line  template.HTML
	Note  template.HTML
	Class string
}

type holderPage struct {
	View    string
	NA      template.HTML
	Script  template.JS
	CSS     template.CSS
	Title   template.HTML
	Sub     template.HTML
	Intro   template.HTML
	Shared  bool
	Secs    []pageSection
	Tell    []template.HTML
	TellTop template.HTML
	// frame 3
	Waiting, WaitingEmpty          template.HTML
	ChoosingTitle, ChoosingNote    template.HTML
	NoTitle                        template.HTML
	NoItems                        []template.HTML
	TheirNoTitle                   template.HTML
	ListTitle, ListNote, ListEmpty template.HTML
	Count                          int
	Items                          []askedItem
	// frame 9
	Prompt promptView
}

type promptView struct {
	Cards                                                  []pageSection
	Then                                                   []promptThen
	ThenTitle, Get, Reset, OutTitle, Copy, Copied, OutNote template.HTML
	Aria                                                   string
	ReportLine, NothingLine                                string
	NoChanges, OneChange                                   string
}

type promptThen struct {
	ID       string
	Label    template.HTML
	Disabled bool
}

// reason is the holder's words for a refusal code the pack knows, else the
// code as the book sealed it.
func (w holderWording) reason(code string) string {
	if v, ok := w.entries["asked.reason."+code]; ok {
		return v
	}
	return code
}

func (w holderWording) na() template.HTML { return w.text("pill.unavailable") }

func (w holderWording) row(label string, pill template.HTML, class string) pageRow {
	return pageRow{Label: w.text(label), Pill: pill, Class: template.HTML(class)}
}

// sharePage is frame 2. One row has a producer: the public checkpoint answer
// (Always, whatever the policy: Authorize serves it to anyone). The receipt
// row has none here: record_at_completion is validated and reported by
// doctor, but this book's responder never reads it (#239).
func sharePage(w holderWording) holderPage {
	na := func(label string) pageRow { return w.row(label, w.na(), "") }
	secs := []pageSection{
		{Title: w.text("share.anyone.title"), Rows: []pageRow{w.row("share.anyone.proof", w.text("pill.always"), "on")}, Note: w.text("share.anyone.note")},
		{Title: w.text("share.signed.title"), Rows: []pageRow{na("share.signed.card"), na("share.signed.window"), na("share.signed.attach")}, Note: w.text("share.signed.attach.note")},
		{Title: w.text("share.more.title"), Rows: []pageRow{na("share.more.longer"), na("share.more.deals")}, Note: w.text("share.more.note")},
		{Title: w.text("share.dealt.title"), Rows: []pageRow{na("share.dealt.receipt")}, Note: w.text("share.dealt.note")},
		{Title: w.text("share.anon.title"), Rows: []pageRow{na("share.anon.askers"), na("share.anon.contract")}, Note: w.text("share.anon.note")},
	}
	never := pageSection{Title: w.text("share.never.title"), Never: true, Note: w.text("share.never.note")}
	for _, k := range []string{"name", "deals", "amounts", "other", "messages", "rules", "asked", "shop"} {
		never.Rows = append(never.Rows, pageRow{Label: w.text("share.never." + k)})
	}
	page := holderPage{Title: w.text("share.title"), Sub: "", Intro: w.text("share.intro"), Shared: w.shared, Secs: append(secs, never)}
	if !w.shared {
		page.TellTop = w.text("share.tell.title")
		page.Tell = []template.HTML{w.text("share.tell.report")}
	}
	return page
}

// answeredEntry is one sealed evidence_request_answered record, as the book
// holds it. The record names no requester (AnsweredStatement), and a request
// from a caller that never authenticated seals nothing, so the list says what
// the book knows: when, how the asker stood, what it asked and what it got.
type answeredEntry struct {
	At        time.Time
	Statement evidencebook.AnsweredStatement
}

func askedPage(w holderWording, entries []answeredEntry) holderPage {
	page := holderPage{
		Title:   w.text("asked.title"),
		Waiting: w.text("asked.waiting.title"), WaitingEmpty: w.text("asked.waiting.empty"),
		ChoosingTitle: w.text("asked.choosing.title"), ChoosingNote: w.text("asked.choosing.note"),
		NoTitle:      w.text("asked.no.title"),
		NoItems:      []template.HTML{w.text("asked.no.1"), w.text("asked.no.2")},
		TheirNoTitle: w.text("asked.theirno.title"),
		ListTitle:    w.text("asked.list.title"), ListNote: w.text("asked.list.note"), ListEmpty: w.text("asked.list.empty"),
		Count: len(entries),
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.After(entries[j].At) })
	if len(entries) > 0 {
		page.Sub = template.HTML(template.HTMLEscapeString(entries[0].At.UTC().Format("Jan 2006")))
	}
	for _, e := range entries {
		st := e.Statement
		who := w.plain("asked.who.counterparty")
		if st.Relationship != evidencebook.RelationshipCounterparty {
			who = w.plain("asked.who.identified")
		}
		var item askedItem
		date := template.HTMLEscapeString(e.At.UTC().Format("Jan 2"))
		switch {
		case st.Outcome == evidencebook.OutcomeRefusal:
			item = askedItem{Class: "u", Line: template.HTML(date + " · " + template.HTMLEscapeString(who) + " · <b>" + template.HTMLEscapeString(w.plain("asked.what.refused")) + "</b>"),
				Note: w.text("asked.what.refused.note", "reason", w.reason(st.Reason))}
		case st.SubjectKind == evidencebook.SubjectCheckpoints:
			item = askedItem{Class: "u", Line: template.HTML(date + " · " + template.HTMLEscapeString(who) + " · " + template.HTMLEscapeString(w.plain("asked.got")) + " <b>" + template.HTMLEscapeString(w.plain("asked.what.checkpoints")) + "</b>"),
				Note: w.text("asked.what.checkpoints.note")}
		default:
			item = askedItem{Class: "g", Line: template.HTML(date + " · " + template.HTMLEscapeString(who) + " · " + template.HTMLEscapeString(w.plain("asked.got")) + " <b>" + template.HTMLEscapeString(w.plain("asked.what.answer")) + "</b>"),
				Note: w.text("asked.what.answer.note", "kind", string(st.SubjectKind))}
		}
		page.Items = append(page.Items, item)
	}
	return page
}

// promptPage is frame 9. Every control the canvas draws is drawn; those with
// no setting in the book are disabled and say so, and the prompt carries only
// the live ones: the report. No policy-change block is written, since no
// producer reads one.
func promptPage(w holderWording) holderPage {
	na := w.na()
	disabled := func(label string) pageRow { return pageRow{Label: w.text(label), Pill: na} }
	return holderPage{
		Title: w.text("prompt.title"), Sub: w.text("prompt.sub"), Intro: w.text("prompt.intro"),
		Prompt: promptView{
			Cards: []pageSection{
				{Title: w.text("prompt.card.title"), Rows: []pageRow{disabled("prompt.card.who"), disabled("prompt.card.window"), disabled("prompt.card.attach")}},
				{Title: w.text("prompt.more.title"), Rows: []pageRow{disabled("prompt.more.longer"), disabled("prompt.more.deals"), disabled("prompt.more.anon")}, Note: w.text("prompt.more.note")},
			},
			Then: []promptThen{
				{ID: "rebuild", Label: w.text("prompt.then.rebuild"), Disabled: true},
				{ID: "report", Label: w.text("prompt.then.report")},
			},
			ThenTitle: w.text("prompt.then.title"), Get: w.text("prompt.get"), Reset: w.text("prompt.reset"), OutTitle: w.text("prompt.out.title"), Copy: w.text("prompt.out.copy"), Copied: w.text("prompt.out.copied"), OutNote: w.text("prompt.out.note"),
			Aria: w.plain("prompt.out.aria"), ReportLine: w.plain("prompt.out.report"), NothingLine: w.plain("prompt.out.nothing"),
			NoChanges: w.plain("prompt.out.none"), OneChange: w.plain("prompt.out.one"),
		},
	}
}

var holderTemplate = template.Must(template.New("holder").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>{{.CSS}}</style></head>
<body><div class="page{{if eq .View "prompt"}} form{{end}}" data-view="{{.View}}">
<div class="top"><span class="t">{{.Title}}</span>{{if .Sub}}<span class="s">{{.Sub}}</span>{{end}}</div>
<div class="body">
{{- if .Intro}}
<div class="sec"><div class="sm">{{.Intro}}</div></div>{{end}}
{{- range .Secs}}{{$never := .Never}}
<div class="sec{{if .Never}} never-sec{{end}}"><div class="h"><span>{{.Title}}</span></div>
{{- range .Rows}}{{if $never}}<div class="never"><b>✕</b><span>{{.Label}}</span></div>{{else}}<div class="row"><span>{{.Label}}</span><span class="pill {{.Class}}">{{.Pill}}</span></div>{{end}}{{end}}
{{- if .Note}}<div class="sm">{{.Note}}</div>{{end}}</div>
{{- end}}
{{- if .Tell}}
<div class="say"><small>{{.TellTop}}</small>{{range .Tell}}<span class="q">{{.}}</span>{{end}}</div>{{end}}
{{- if eq .View "asked"}}
<div class="sec"><div class="h"><span>{{.Waiting}}</span></div><div class="sm">{{.WaitingEmpty}}</div></div>
<div class="sec"><div class="h"><span>{{.ChoosingTitle}}</span><span class="tg o">▾</span></div><div class="sm">{{.ChoosingNote}}</div></div>
<div class="sec"><div class="h"><span>{{.NoTitle}}</span><span class="tg o">▾</span></div>{{range .NoItems}}<div class="it"><span class="dot y"></span><div>{{.}}</div></div>{{end}}</div>
<div class="sec"><div class="h"><span>{{.TheirNoTitle}}</span><span class="pill">{{.NA}}</span><span class="tg">▸</span></div></div>
<div class="sec"><div class="h"><span>{{.ListTitle}}</span><span class="pill">{{.Count}}</span><span class="tg o">▾</span></div>
{{- range .Items}}<div class="it"><span class="dot {{.Class}}"></span><div>{{.Line}}<div class="sm">{{.Note}}</div></div></div>{{end}}
{{- if not .Items}}<div class="sm">{{.ListEmpty}}</div>{{end}}
<div class="sm">{{.ListNote}}</div></div>
{{- end}}
{{- if eq .View "prompt"}}
{{- range .Prompt.Cards}}
<div class="sec"><div class="h">{{.Title}}</div>
{{- range .Rows}}<div class="row"><label>{{.Label}}</label><select disabled><option>{{.Pill}}</option></select></div>{{end}}
{{- if .Note}}<div class="sm">{{.Note}}</div>{{end}}</div>
{{- end}}
<div class="sec"><div class="h">{{.Prompt.ThenTitle}}</div>
{{- range .Prompt.Then}}<label class="chk{{if .Disabled}} na{{end}}"><input type="checkbox" id="{{.ID}}"{{if .Disabled}} disabled{{end}}> {{.Label}}{{if .Disabled}} <span class="pill">{{$.NA}}</span>{{end}}</label>{{end}}</div>
<div><button id="get" type="button">{{.Prompt.Get}}</button> <button id="reset" type="button" class="q">{{.Prompt.Reset}}</button></div>
<div class="sec" id="out" hidden style="border-color:#2B5A85"><div style="display:flex;align-items:center;gap:8px"><span class="h">{{.Prompt.OutTitle}}</span><span class="pill" id="count"></span><span class="pill ok" id="copied" hidden>{{.Prompt.Copied}}</span></div>
<textarea id="prompt" rows="6" readonly aria-label="{{.Prompt.Aria}}" data-report="{{.Prompt.ReportLine}}" data-nothing="{{.Prompt.NothingLine}}" data-single="{{.Prompt.OneChange}}" data-none="{{.Prompt.NoChanges}}"></textarea>
<button id="copy" type="button">{{.Prompt.Copy}}</button><div class="sm">{{.Prompt.OutNote}}</div></div>
<script>{{.Script}}</script>
{{- end}}
</div></div></body></html>
`))

// promptScript builds the prompt from the live checkboxes only. It reads the
// page's own data-* strings so no wording sits in the script.
const promptScript = `
(function(){
  var $=function(i){return document.getElementById(i)};
  var report=$("report"), out=$("out"), box=$("prompt"), copied=$("copied");
  function lines(){ return report.checked ? [box.dataset.report] : []; }
  function hide(){ out.hidden=true; copied.hidden=true; }
  report.addEventListener("change",hide);
  $("get").addEventListener("click",function(){
    var l=lines();
    box.value=l.length?l.join("\n"):box.dataset.nothing;
    $("count").textContent=l.length?box.dataset.single:box.dataset.none;
    copied.hidden=true; out.hidden=false;
  });
  $("reset").addEventListener("click",function(){ report.checked=false; hide(); });
  $("copy").addEventListener("click",function(){
    function done(){ copied.hidden=false; }
    function fallback(){ box.focus(); box.select(); try{document.execCommand("copy");}catch(e){} done(); }
    if(navigator.clipboard&&navigator.clipboard.writeText){ navigator.clipboard.writeText(box.value).then(done,fallback); } else { fallback(); }
  });
})();`

// buildHolderPage renders one view to a self-contained page.
func buildHolderPage(view string, w holderWording, entries []answeredEntry) ([]byte, error) {
	var page holderPage
	switch view {
	case viewShare:
		page = sharePage(w)
	case viewAsked:
		page = askedPage(w, entries)
	case viewPrompt:
		page = promptPage(w)
	default:
		return nil, cli.InputError("--view is share, asked or prompt")
	}
	page.View, page.NA, page.CSS, page.Script = view, w.na(), template.CSS(holderPagesCSS), template.JS(promptScript)
	var buf bytes.Buffer
	if err := holderTemplate.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// answeredEntries are the book's sealed evidence_request_answered records.
func answeredEntries(c *cobra.Command, p cli.Profile) ([]answeredEntry, error) {
	opened, err := openBook(c.Context(), p, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.release() }()
	records, err := opened.book.Query(c.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeRequestAnswered})
	if err != nil {
		return nil, bookError(err)
	}
	var entries []answeredEntry
	for _, r := range records {
		var st evidencebook.AnsweredStatement
		if err := json.Unmarshal(r.Header.Statement, &st); err != nil {
			return nil, fmt.Errorf("record %s is not an answered-request statement: %w", r.RecordID, err)
		}
		at, err := time.Parse(time.RFC3339, r.Header.CommittedAt)
		if err != nil {
			return nil, fmt.Errorf("record %s has no committed_at time: %w", r.RecordID, err)
		}
		entries = append(entries, answeredEntry{At: at, Statement: st})
	}
	return entries, nil
}

type holderPageResult struct {
	View   string `json:"view"`
	Shared bool   `json:"shared"`
	Out    string `json:"out"`
	// Wording is the id of the pack the page's words come from, and the
	// SHA-256 of the pack's bytes.
	Wording       string `json:"wording"`
	WordingSHA256 string `json:"wording_sha256"`
	Answered      int    `json:"answered,omitempty"`
}

// holderPageCommand is `capsulectl book holder-page`: the holder's own pages
// of Steven's 9b canvas, written as one offline file. It seals nothing. The
// asked view reads the book; the other views read nothing.
func wordingPathOf(c *cobra.Command) string { p, _ := c.Flags().GetString("wording"); return p }
func wordingDigestOf(c *cobra.Command) string {
	d, _ := c.Flags().GetString("wording-digest")
	return d
}

func holderPageCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "holder-page", Short: "Write one of the holder's own pages (what I share, who asked, settings to prompt) as an offline page, from the book; seals nothing", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		view, _ := c.Flags().GetString("view")
		out, _ := c.Flags().GetString("out")
		shared, _ := c.Flags().GetBool("shared")
		if out == "" {
			return cli.InputError("--out is required")
		}
		if shared && view != viewShare {
			return cli.InputError("--shared applies to --view share only: the asks and the prompt are the holder's alone, and a shared asks page would tell a third party who asked about the holder")
		}
		pack := holderWordingJSON
		custom, err := readWording(wordingPathOf(c), wordingDigestOf(c))
		if err != nil {
			return err
		}
		if custom != nil {
			digest := sha256.Sum256([]byte(custom.Pack))
			if hex.EncodeToString(digest[:]) != custom.SHA256 {
				return cli.InputError("--wording-digest is not the SHA-256 of the --wording file's bytes; no page is written")
			}
			pack = []byte(custom.Pack)
		}
		entries, err := decodeHolderWording(pack, shared)
		if err != nil {
			return cli.InputError(err.Error())
		}
		var meta struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(pack, &meta)
		digest := sha256.Sum256(pack)
		id, sum := meta.ID, hex.EncodeToString(digest[:])

		var answered []answeredEntry
		if view == viewAsked {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			if !isBook(p) {
				return cli.InputError("--view asked reads an evidence book: --profile is not one")
			}
			if answered, err = answeredEntries(c, p); err != nil {
				return err
			}
		}
		page, err := buildHolderPage(view, holderWording{entries: entries, shared: shared}, answered)
		if err != nil {
			return err
		}
		if err = cli.AtomicFile(out, page, false); errors.Is(err, os.ErrExist) {
			return cli.Hint(cli.ErrInput, fmt.Sprintf("--out %s already exists (or was created by another process while this one ran); nothing was overwritten; pass another path", out))
		} else if err != nil {
			return err
		}
		return cli.Output(c, holderPageResult{View: view, Shared: shared, Out: out, Wording: id, WordingSHA256: sum, Answered: len(answered)})
	}}
	cmd.Flags().String("view", viewShare, "share (What I share), asked (They want more) or prompt (Settings to prompt)")
	cmd.Flags().Bool("shared", false, "Write the page in the third person, for a copy the holder shares; --view share only")
	cmd.Flags().String("wording", "", "An aac.wording-pack/v0 pack with every entry the pages draw, in place of the embedded pack; needs --wording-digest")
	cmd.Flags().String("wording-digest", "", "Lowercase hex SHA-256 of the --wording file's bytes")
	cmd.Flags().String("out", "", "Write the page here (new file)")
	return cmd
}
