package singlehost

func SessionRepoGitignorePatterns() []string {
	return []string{
		"*.sock",
		"*.socket",
		"*.pid",
		"*.lock",
		"*.flock",
	}
}
