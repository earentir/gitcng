package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var metricNames = []string{
	metricUncommitted,
	metricLocal,
	metricRemote,
	metricAhead,
	metricConflicts,
	metricDiverged,
	metricErrors,
	metricAttention,
	metricRepos,
}

func registerCompletions(cmd *cobra.Command) {
	cmd.ValidArgsFunction = completeScanPath

	_ = cmd.RegisterFlagCompletionFunc("count", completeMetrics)
	_ = cmd.RegisterFlagCompletionFunc("only", completeMetrics)
	_ = cmd.RegisterFlagCompletionFunc("colour", completeColourMode)
}

func completeScanPath(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveFilterDirs
}

func completeMetrics(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return prefixMatch(metricNames, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeColourMode(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return prefixMatch([]string{"auto", "always", "never"}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func prefixMatch(candidates []string, toComplete string) []string {
	if toComplete == "" {
		return candidates
	}
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, toComplete) {
			out = append(out, c)
		}
	}
	return out
}
