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
	_ = cmd.RegisterFlagCompletionFunc("format", completeOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("depth", completeDepth)
	_ = cmd.RegisterFlagCompletionFunc("jobs", completeJobs)
	_ = cmd.RegisterFlagCompletionFunc("fetch-timeout", completeFetchTimeout)
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

func completeOutputFormat(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return prefixMatch([]string{"text", "json"}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeDepth(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if toComplete != "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"1", "2", "3", "4", "6", "8"}, cobra.ShellCompDirectiveNoFileComp
}

func completeJobs(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if toComplete != "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"0", "1", "4", "8", "16"}, cobra.ShellCompDirectiveNoFileComp
}

func completeFetchTimeout(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if toComplete != "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"5", "10", "15", "30", "60"}, cobra.ShellCompDirectiveNoFileComp
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
