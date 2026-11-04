package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

func runConfig(args []string, s IO) int {
	if len(args) == 0 || args[0] != "show" {
		return usageError(s, "usage: config show --effective")
	}
	fset := newFlagSet("config show", s)
	effective := fset.Bool("effective", false, "print the effective value and source of each key")
	pos, err := parseFlags(fset, args[1:])
	if err != nil || len(pos) > 0 || !*effective {
		return usageError(s, "usage: config show --effective")
	}
	sc, err := resolveScope(s)
	if err != nil {
		return fail(s, err)
	}
	w := tabwriter.NewWriter(s.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE\tSOURCE")
	for _, e := range sc.cfg.Effective() {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, e.Value, e.Source)
	}
	return flushOK(s, w)
}

// setConfigStorage sets the storage key of a project config file, keeping the
// other keys and comments. A missing file is created from the template.
func setConfigStorage(path, storage string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(configTemplate(storage)), configFileMode)
	}
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level must be a mapping", path)
	}
	m := doc.Content[0]
	found := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "storage" {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: storage}
			found = true
		}
	}
	if !found {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "storage"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: storage})
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, configFileMode)
}
