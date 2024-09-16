package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func init() {
	var docsPath string

	var docgenCmd = &cobra.Command{
		Use:    "docgen",
		Short:  "Generation documentation for the command line",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			err := doc.GenMarkdownTree(rootCmd, docsPath)
			if err != nil {
				return err
			}

			return nil
		},
	}

	docgenCmd.Flags().StringVar(&docsPath, "out", "./docs/", "directory to write generated CLI documentation to")

	rootCmd.AddCommand(docgenCmd)
}
