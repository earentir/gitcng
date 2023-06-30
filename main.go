package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/plumbing/storer"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type RepoStatus struct {
	Path          string
	LocalChanges  int
	RemoteChanges int
}

var maxDepth int
var repos []RepoStatus
var rootPath string // global rootPath variable

func getSignersFromAgent() ([]ssh.Signer, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		return nil, err
	}
	return signers, nil
}

func countRemoteChanges(r *git.Repository, headRef *plumbing.Reference, remoteRef *plumbing.Reference) (int, error) {
	// No remote changes if the hashes are the same
	if headRef.Hash() == remoteRef.Hash() {
		return 0, nil
	}

	cIter, err := r.Log(&git.LogOptions{From: remoteRef.Hash()})
	if err != nil {
		return 0, err
	}

	remoteChanges := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == headRef.Hash() {
			// Stop iteration when we reach the local HEAD
			return storer.ErrStop
		}
		remoteChanges++
		return nil
	})

	if err != nil && err != storer.ErrStop {
		return 0, err
	}

	return remoteChanges, nil
}

func visitAndCheckGit(path string, depth int) {
	if depth > maxDepth {
		return
	}
	log.Printf("Visiting path: %s at depth: %d\n", path, depth)
	// checking if current directory is a git repository
	r, err := git.PlainOpen(path)
	if err == nil {
		log.Printf("Checking path: %s\n", path)

		w, err := r.Worktree()
		if err != nil {
			log.Printf("Error getting worktree for path %s: %s\n", path, err)
			return
		}

		status, err := w.Status()
		if err != nil {
			log.Printf("Error getting status for path %s: %s\n", path, err)
			return
		}

		localChanges := countChanges(status)

		signers, err := getSignersFromAgent()
		if err != nil {
			log.Printf("Error getting signers from agent: %v\n", err)
			return
		}

		if len(signers) == 0 {
			log.Printf("No SSH keys loaded in ssh-agent")
			return
		}

		// get the remote origin URL to extract the username
		remotes, err := r.Remotes()
		if err != nil {
			log.Printf("Error getting remotes for path %s: %s\n", path, err)
			return
		}

		var username string
		for _, remote := range remotes {
			if remote.Config().Name == "origin" {
				remoteURL := remote.Config().URLs[0]
				if strings.HasPrefix(remoteURL, "git@") {
					parts := strings.Split(remoteURL, "@")
					if len(parts) > 1 {
						username = parts[0]
					}
				} else {
					u, err := url.Parse(remoteURL)
					if err != nil {
						log.Printf("Error parsing URL for path %s: %s\n", path, err)
						return
					}
					username = u.User.Username()
				}
			}
		}

		// if no username found, fall back to "git"
		if username == "" {
			username = "git"
		}

		auth, err := gogitssh.NewSSHAgentAuth(username)
		if err != nil {
			log.Printf("Error creating auth object: %v\n", err)
			return
		}

		auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()

		// fetch latest commits
		err = r.Fetch(&git.FetchOptions{
			RemoteName: "origin",
			Progress:   nil,
			Auth:       auth,
		})

		if err != nil && err != git.NoErrAlreadyUpToDate {
			log.Printf("Error fetching from origin for path %s: %s\n", path, err)
			return
		}

		// get HEAD reference
		headRef, err := r.Head()
		if err != nil {
			log.Printf("Error getting HEAD for path %s: %s\n", path, err)
			return
		}

		// get remote reference
		remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", headRef.Name().Short()), true)
		if err != nil {
			log.Printf("Error getting remote reference for path %s: %s\n", path, err)
			return
		}

		remoteChanges, err := countRemoteChanges(r, headRef, remoteRef)
		if err != nil {
			log.Printf("Error counting remote changes for path %s: %s\n", path, err)
			return
		}

		repos = append(repos, RepoStatus{Path: path, LocalChanges: localChanges, RemoteChanges: remoteChanges})

		// return here because we don't need to check subdirectories if this is a git repo
		return
	}

	// getting subdirectories
	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Printf("Error reading directory %s: %s\n", path, err)
		return
	}

	for _, f := range files {
		if f.IsDir() && f.Mode()&os.ModeSymlink != os.ModeSymlink {
			visitAndCheckGit(filepath.Join(path, f.Name()), depth+1)
		}
	}
}

func main() {
	maxDepthPtr := flag.Int("depth", 4, "the maximum depth")
	flag.Parse()

	maxDepth = *maxDepthPtr

	args := flag.Args()
	if len(args) > 0 {
		rootPath = args[0]
	} else {
		rootPath = "." // default to the current directory
	}

	visitAndCheckGit(rootPath, 0)

	// Print each repository only once
	for _, repo := range repos {
		fmt.Printf("In Folder [%s]: git present\n", repo.Path)
		fmt.Println("Local Changes:", repo.LocalChanges)
		fmt.Println("Remote Changes:", repo.RemoteChanges)
	}
}

func countChanges(status git.Status) int {
	changes := 0
	for _, s := range status {
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			changes++
		}
	}
	return changes
}
