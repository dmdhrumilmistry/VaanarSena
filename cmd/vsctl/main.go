// Command vsctl drives VaanarSena from the command line and CI pipelines:
// declarative apply/diff/export of manifests, plus device and group helpers.
//
//	export VS_SERVER=https://mdm.example.com VS_TOKEN=vsat_...
//	vsctl apply -f config/            # files, directories or - for stdin
//	vsctl diff -f config/             # what apply would change
//	vsctl export > fleet.yaml
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

var version = "dev"

const usage = `vsctl - VaanarSena command line

Environment: VS_SERVER (base URL), VS_TOKEN (API token), VS_CA_FILE (optional PEM)

Commands:
  apply   -f PATH [-f PATH...] [--dry-run] [--prune] [--owner NAME]
  diff    -f PATH [-f PATH...] [--prune] [--owner NAME]
  export  [--format yaml|json]
  get     devices|groups|policies|blueprints [-o json] [--platform P] [--tag T] [--group ID]
  preview -f RULES.yaml           evaluate smart group rules without saving
  tag     DEVICE_ID [TAG...]      replace a device's tags (no tags clears them)
  command DEVICE_ID TYPE [--params JSON]
  version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "apply":
		err = apply(args, false)
	case "diff":
		err = apply(args, true)
	case "export":
		err = export(args)
	case "get":
		err = get(args)
	case "preview":
		err = preview(args)
	case "tag":
		err = tag(args)
	case "command":
		err = sendCommand(args)
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", os.Args[1], usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient() (*client, error) {
	base, token := strings.TrimRight(os.Getenv("VS_SERVER"), "/"), os.Getenv("VS_TOKEN")
	if base == "" || token == "" {
		return nil, errors.New("set VS_SERVER and VS_TOKEN (create a token under Account in the console)")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ca := os.Getenv("VS_CA_FILE"); ca != "" {
		pool, err := loadPool(ca)
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	return &client{base: base, token: token, http: &http.Client{Transport: tr, Timeout: 2 * time.Minute}}, nil
}

func (c *client) do(method, path, contentType string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequest(method, c.base+"/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return out, resp.StatusCode, err
}

// apiError renders an error response, including validation problems.
func apiError(code int, body []byte) error {
	var e struct {
		Error    string   `json:"error"`
		Problems []string `json:"problems"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		return fmt.Errorf("HTTP %d: %s", code, bytes.TrimSpace(body))
	}
	msg := e.Error
	for _, p := range e.Problems {
		msg += "\n  - " + p
	}
	return errors.New(msg)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// readManifests concatenates files (directories are walked) as YAML documents.
func readManifests(paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return nil, errors.New("-f is required")
	}
	var buf bytes.Buffer
	add := func(data []byte, name string) error {
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM from Windows tools
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 {
			return nil
		}
		if trimmed[0] == '{' || trimmed[0] == '[' {
			// JSON is valid YAML; re-encode so documents join cleanly.
			var v any
			if err := json.Unmarshal(trimmed, &v); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if items, ok := v.([]any); ok {
				for _, it := range items {
					b, _ := yaml.Marshal(it)
					buf.WriteString("---\n")
					buf.Write(b)
				}
				return nil
			}
			if m, ok := v.(map[string]any); ok {
				if items, ok := m["items"].([]any); ok {
					for _, it := range items {
						b, _ := yaml.Marshal(it)
						buf.WriteString("---\n")
						buf.Write(b)
					}
					return nil
				}
			}
			b, _ := yaml.Marshal(v)
			data = b
		}
		buf.WriteString("---\n")
		buf.Write(data)
		buf.WriteString("\n")
		return nil
	}
	for _, p := range paths {
		if p == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, err
			}
			if err := add(data, "stdin"); err != nil {
				return nil, err
			}
			continue
		}
		var files []string
		err := filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".yaml", ".yml", ".json":
				if !info.IsDir() {
					files = append(files, path)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if err := add(data, f); err != nil {
				return nil, err
			}
		}
	}
	return buf.Bytes(), nil
}

func apply(args []string, diff bool) error {
	name := "apply"
	if diff {
		name = "diff"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var files multiFlag
	fs.Var(&files, "f", "manifest file, directory or - (repeatable)")
	dry := fs.Bool("dry-run", false, "validate and show changes without applying")
	prune := fs.Bool("prune", false, "delete resources owned by --owner that are not in the manifests")
	owner := fs.String("owner", "manifest", "ownership label scoping --prune")
	_ = fs.Parse(args)
	body, err := readManifests(files)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	q := url.Values{"owner": {*owner}}
	if *dry || diff {
		q.Set("dryRun", "true")
	}
	if *prune {
		q.Set("prune", "true")
	}
	out, code, err := c.do(http.MethodPost, "/apply?"+q.Encode(), "application/yaml", body)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return apiError(code, out)
	}
	var res struct {
		DryRun  bool `json:"dryRun"`
		Changes []struct {
			Kind, Name, Action string
		} `json:"changes"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	counts := map[string]int{}
	for _, ch := range res.Changes {
		counts[ch.Action]++
		if ch.Action == "unchanged" && !diff {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s/%s\n", ch.Action, ch.Kind, ch.Name)
	}
	_ = tw.Flush()
	suffix := ""
	if res.DryRun {
		suffix = " (dry run, nothing changed)"
	}
	fmt.Printf("%d created, %d updated, %d deleted, %d unchanged%s\n",
		counts["created"], counts["updated"], counts["deleted"], counts["unchanged"], suffix)
	return nil
}

func export(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "yaml", "yaml or json")
	_ = fs.Parse(args)
	c, err := newClient()
	if err != nil {
		return err
	}
	out, code, err := c.do(http.MethodGet, "/export?format="+url.QueryEscape(*format), "", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return apiError(code, out)
	}
	_, err = os.Stdout.Write(out)
	return err
}

func get(args []string) error {
	if len(args) == 0 {
		return errors.New("get needs a resource: devices, groups, policies or blueprints")
	}
	res := args[0]
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	output := fs.String("o", "table", "table or json")
	platform := fs.String("platform", "", "filter devices by platform")
	tagF := fs.String("tag", "", "filter devices by tag")
	group := fs.String("group", "", "filter devices by group ID")
	_ = fs.Parse(args[1:])
	c, err := newClient()
	if err != nil {
		return err
	}
	path := "/" + res
	if res == "devices" {
		q := url.Values{"limit": {"500"}}
		for k, v := range map[string]string{"platform": *platform, "tag": *tagF, "group": *group} {
			if v != "" {
				q.Set(k, v)
			}
		}
		path += "?" + q.Encode()
	}
	out, code, err := c.do(http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return apiError(code, out)
	}
	if *output == "json" {
		_, err = os.Stdout.Write(out)
		return err
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		return err
	}
	cols := map[string][]string{
		"devices":    {"id", "name", "platform", "ownership", "status", "osVersion", "tags", "lastSeenAt"},
		"groups":     {"id", "name", "kind", "deviceCount", "managedBy"},
		"policies":   {"id", "name", "priority", "version", "managedBy"},
		"blueprints": {"id", "name", "priority", "version", "managedBy"},
	}[res]
	if cols == nil {
		return fmt.Errorf("unknown resource %q", res)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(cols, "\t")))
	for _, row := range body[res] {
		vals := make([]string, len(cols))
		for i, col := range cols {
			vals[i] = cell(row[col])
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	return tw.Flush()
}

func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = fmt.Sprint(x)
		}
		return strings.Join(parts, ",")
	case float64:
		return fmt.Sprint(t)
	}
	return fmt.Sprint(v)
}

func preview(args []string) error {
	fs := flag.NewFlagSet("preview", flag.ExitOnError)
	file := fs.String("f", "", "rules file (YAML or JSON)")
	_ = fs.Parse(args)
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return err
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	out, code, err := c.do(http.MethodPost, "/groups/preview", "application/json", body)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return apiError(code, out)
	}
	var res struct {
		Total   int              `json:"total"`
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	for _, d := range res.Devices {
		fmt.Printf("%s\t%s\t%s\t%s\n", cell(d["id"]), cell(d["name"]), cell(d["platform"]), cell(d["osVersion"]))
	}
	fmt.Printf("%d device(s) match\n", res.Total)
	return nil
}

func tag(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vsctl tag DEVICE_ID [TAG...]")
	}
	body, _ := json.Marshal(map[string][]string{"tags": append([]string{}, args[1:]...)})
	c, err := newClient()
	if err != nil {
		return err
	}
	out, code, err := c.do(http.MethodPut, "/devices/"+url.PathEscape(args[0])+"/tags", "application/json", body)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return apiError(code, out)
	}
	fmt.Println("tags updated")
	return nil
}

func sendCommand(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: vsctl command DEVICE_ID TYPE [--params JSON]")
	}
	fs := flag.NewFlagSet("command", flag.ExitOnError)
	params := fs.String("params", "{}", "command parameters as JSON")
	_ = fs.Parse(args[2:])
	if !json.Valid([]byte(*params)) {
		return errors.New("--params is not valid JSON")
	}
	body, _ := json.Marshal(map[string]any{"type": args[1], "params": json.RawMessage(*params)})
	c, err := newClient()
	if err != nil {
		return err
	}
	out, code, err := c.do(http.MethodPost, "/devices/"+url.PathEscape(args[0])+"/commands", "application/json", body)
	if err != nil {
		return err
	}
	if code != http.StatusAccepted {
		return apiError(code, out)
	}
	var cmd struct{ ID, Status string }
	_ = json.Unmarshal(out, &cmd)
	fmt.Printf("queued %s (%s)\n", cmd.ID, cmd.Status)
	return nil
}
