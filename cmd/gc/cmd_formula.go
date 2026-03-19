package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/formula"
	"github.com/spf13/cobra"
)

func newFormulaCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "formula",
		Short: "Manage and inspect formulas",
	}

	cmd.AddCommand(newFormulaListCmd(stdout))
	cmd.AddCommand(newFormulaShowCmd(stdout, stderr))
	return cmd
}

func newFormulaListCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available formulas",
		Long: `List all formulas available in the city's formula search paths.

Formulas are discovered from city-level and rig-level formula directories
configured via packs and formulas_dir settings.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			paths := cityFormulaSearchPaths()
			if len(paths) == 0 {
				fmt.Fprintln(stdout, "No formula search paths configured.")
				return nil
			}

			// Scan search paths for .formula.toml files, deduplicating by name
			// (last path wins, matching formula layer resolution order).
			winners := make(map[string]string)
			for _, dir := range paths {
				entries, err := os.ReadDir(dir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".formula.toml") {
						continue
					}
					name := strings.TrimSuffix(e.Name(), ".formula.toml")
					winners[name] = filepath.Join(dir, e.Name())
				}
			}

			if len(winners) == 0 {
				fmt.Fprintln(stdout, "No formulas found.")
				return nil
			}

			names := make([]string, 0, len(winners))
			for name := range winners {
				names = append(names, name)
			}
			sortStrings(names)

			for _, name := range names {
				fmt.Fprintln(stdout, name)
			}
			return nil
		},
	}
}

func newFormulaShowCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <formula-name>",
		Short: "Show a compiled formula recipe",
		Long: `Compile and display a formula recipe.

By default, shows the recipe with {{variable}} placeholders intact.
Use --var to substitute variables and preview the resolved output.

Examples:
  gc formula show mol-feature
  gc formula show mol-feature --var title="Auth system" --var branch=main`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			varFlags, _ := cmd.Flags().GetStringArray("var")

			vars := make(map[string]string, len(varFlags))
			for _, v := range varFlags {
				key, value, ok := strings.Cut(v, "=")
				if ok && key != "" {
					vars[key] = value
				}
			}

			var compileVars map[string]string
			if len(vars) > 0 {
				compileVars = vars
			}

			recipe, err := formula.Compile(context.Background(), name, cityFormulaSearchPaths(), compileVars)
			if err != nil {
				return err
			}

			// Apply var substitution for display if vars provided.
			displayVars := formula.ApplyDefaults(
				&formula.Formula{Vars: recipe.Vars},
				vars,
			)

			fmt.Fprintf(stdout, "Formula: %s\n", recipe.Name)
			if recipe.Description != "" {
				desc := recipe.Description
				if len(displayVars) > 0 {
					desc = formula.Substitute(desc, displayVars)
				}
				fmt.Fprintf(stdout, "Description: %s\n", desc)
			}
			if recipe.Phase != "" {
				fmt.Fprintf(stdout, "Phase: %s\n", recipe.Phase)
			}
			if recipe.RootOnly {
				fmt.Fprintln(stdout, "Root only: true")
			}
			if len(recipe.Vars) > 0 {
				fmt.Fprintln(stdout, "\nVariables:")
				for vname, def := range recipe.Vars {
					var attrs []string
					if def.Required {
						attrs = append(attrs, "required")
					}
					if def.Default != nil {
						attrs = append(attrs, "default="+*def.Default)
					}
					attrStr := ""
					if len(attrs) > 0 {
						attrStr = " (" + strings.Join(attrs, ", ") + ")"
					}
					fmt.Fprintf(stdout, "  {{%s}}: %s%s\n", vname, def.Description, attrStr)
				}
			}

			fmt.Fprintf(stdout, "\nSteps (%d):\n", len(recipe.Steps))
			for i, step := range recipe.Steps {
				if step.IsRoot {
					continue
				}
				title := step.Title
				if len(displayVars) > 0 {
					title = formula.Substitute(title, displayVars)
				}

				typeStr := ""
				if step.Type != "" && step.Type != "task" {
					typeStr = fmt.Sprintf(" (%s)", step.Type)
				}

				var blockDeps []string
				for _, dep := range recipe.Deps {
					if dep.StepID == step.ID && dep.Type == "blocks" {
						blockDeps = append(blockDeps, dep.DependsOnID)
					}
				}
				depStr := ""
				if len(blockDeps) > 0 {
					depStr = fmt.Sprintf(" [needs: %s]", strings.Join(blockDeps, ", "))
				}

				connector := "├──"
				if i == len(recipe.Steps)-1 {
					connector = "└──"
				}

				fmt.Fprintf(stdout, "  %s %s: %s%s%s\n", connector, step.ID, title, typeStr, depStr)
			}

			_ = stderr // reserved for warnings
			return nil
		},
	}

	cmd.Flags().StringArray("var", nil, "variable substitution for preview (key=value)")
	return cmd
}

// cityFormulaSearchPaths returns the city-level formula search paths.
// Best-effort: returns nil if no city is loaded.
func cityFormulaSearchPaths() []string {
	cityPath, err := resolveCity()
	if err != nil {
		return nil
	}
	cfg, err := loadCityConfig(cityPath)
	if err != nil {
		return nil
	}
	return cfg.FormulaLayers.City
}

// sortStrings sorts a string slice in place (insertion sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
