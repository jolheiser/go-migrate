package main

import (
	"fmt"
	"github.com/BurntSushi/toml"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
)

type Project struct {
	SVN      string `toml:"svn"`
	Name     string `toml:"name"`
	Standard bool   `toml:"std"`
}

type Config struct {
	BasePath  string    `toml:"base_path"`
	UsersPath string    `toml:"users_path"`
	BashPath  string    `toml:"bash_path"`
	Projects  []Project `toml:"projects"`
}

type Queue struct {
	wg sync.WaitGroup
	Complete int
	Total int
}

func (q *Queue) Add(delta int) {
	q.wg.Add(delta)
	q.Total++
}

func (q *Queue) Done() {
	q.wg.Done()
	q.Complete++
}

var (
	queue  = &Queue{}
	mu     sync.Mutex
	config Config
)

func main() {
	_, _ = toml.DecodeFile("projects.toml", &config)

	if err := os.Chdir(config.BasePath); err != nil {
		fmt.Printf("Could not change directory: %v\n", err)
		os.Exit(1)
	}

	if err := checkAssets(); err != nil {
		fmt.Printf("Could not generate assets: %v\n", err)
		os.Exit(1)
	}

	for _, project := range config.Projects {
		queue.Add(1)
		go migrate(project)
	}

	queue.wg.Wait()
	fmt.Println("Migration finished...")
}

func migrate(project Project) {
	defer func() {
		queue.Done()
		fmt.Printf("[%d/%d] Finished migrating %s\n", queue.Complete, queue.Total, project.Name)
	}()

	if _, err := os.Stat(path.Join(config.BasePath, project.Name)); err == nil {
		fmt.Printf("%s already exists, skipping...\n", project.Name)
		return
	}

	std := ""
	if project.Standard {
		std = "-s"
	}

	out, err := os.Create(path.Join(config.BasePath, fmt.Sprintf("%s.log", project.Name)))
	if err != nil {
		fmt.Printf("Could not open log file for %s: %v\n", project.Name, err)
		return
	}
	defer out.Close()

	// Migration
	migration := exec.Command("git", "svn", "clone", project.SVN, "--authors-file=users.txt", "--no-metadata", "--prefix", std, project.Name)
	migration.Stdout = out
	migration.Stderr = out
	_, _ = out.WriteString(fmt.Sprintf("%s\n", strings.Join(migration.Args, " ")))
	fmt.Printf("Migrating %s...\n", project.Name)
	if err := migration.Run(); err != nil {
		fmt.Printf("Could not migrate %s: %v\n", project.Name, err)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if err := os.Chdir(path.Join(config.BasePath, project.Name)); err != nil {
		fmt.Printf("Could not change directory: %v\n", err)
		return
	}

	// Cleanup
	// Tags
	tags := exec.Command(config.BashPath, path.Join(config.BasePath, "tags.sh"))
	tags.Stdout = out
	tags.Stderr = out
	_, _ = out.WriteString(fmt.Sprintf("%s\n", strings.Join(tags.Args, " ")))
	fmt.Printf("Converting tags for %s...\n", project.Name)
	if err := tags.Run(); err != nil {
		fmt.Printf("Could not convert tags for %s: %v\n", project.Name, err)
	}

	// Branches
	branches := exec.Command(config.BashPath, path.Join(config.BasePath, "branches.sh"))
	branches.Stdout = out
	branches.Stderr = out
	_, _ = out.WriteString(fmt.Sprintf("%s\n", strings.Join(branches.Args, " ")))
	fmt.Printf("Converting branches for %s...\n", project.Name)
	if err := branches.Run(); err != nil {
		fmt.Printf("Could not convert branches for %s: %v\n", project.Name, err)
	}

	// Peg-revisions
	pegs := exec.Command(config.BashPath, path.Join(config.BasePath, "pegs.sh"))
	pegs.Stdout = out
	pegs.Stderr = out
	_, _ = out.WriteString(fmt.Sprintf("%s\n", strings.Join(pegs.Args, " ")))
	fmt.Printf("Converting peg-revisions for %s...\n", project.Name)
	if err := pegs.Run(); err != nil {
		fmt.Printf("Could not convert the peg-revisions for %s: %v\n", project.Name, err)
	}

	// Standard projects have a trunk branch, otherwise a git-svn branch
	oldBranch := "git-svn"
	if project.Standard {
		oldBranch = "trunk"
	}
	old := exec.Command("git", "branch", "-d", oldBranch)
	old.Stdout = out
	old.Stderr = out
	_, _ = out.WriteString(fmt.Sprintf("%s\n", strings.Join(old.Args, " ")))
	fmt.Printf("Deleting the %s branch...\n", oldBranch)
	if err := old.Run(); err != nil {
		fmt.Printf("Could not delete the %s branch: %v\n", oldBranch, err)
	}

	if err := os.Chdir(config.BasePath); err != nil {
		fmt.Printf("Could not change directory: %v\n", err)
		return
	}
}

func checkAssets() error {
	fit, err := os.Create(path.Join(config.BasePath, "tags.sh"))
	if err != nil {
		return err
	}
	if _, err = fit.WriteString(tagsSh); err != nil {
		return err
	}
	defer fit.Close()

	fib, err := os.Create(path.Join(config.BasePath, "branches.sh"))
	if err != nil {
		return err
	}
	if _, err = fib.WriteString(branchesSh); err != nil {
		return err
	}
	defer fib.Close()

	fip, err := os.Create(path.Join(config.BasePath, "pegs.sh"))
	if err != nil {
		return err
	}
	if _, err = fip.WriteString(pegsSh); err != nil {
		return err
	}
	defer fip.Close()

	fiup, err := os.Open(config.UsersPath)
	if err != nil {
		return err
	}
	defer fiup.Close()

	users, err := ioutil.ReadAll(fiup)
	if err != nil {
		return err
	}

	fiu, err := os.Create(path.Join(config.BasePath, "users.txt"))
	if err != nil {
		return err
	}
	if _, err = fiu.Write(users); err != nil {
		return err
	}
	defer fiu.Close()

	return nil
}

const (
	tagsSh     = `for t in $(git for-each-ref --format='%(refname:short)' refs/remotes/tags); do git tag ${t/tags\//} $t && git branch -D -r $t; done`
	branchesSh = `for b in $(git for-each-ref --format='%(refname:short)' refs/remotes); do git branch $b refs/remotes/$b && git branch -D -r $b; done`
	pegsSh     = `for p in $(git for-each-ref --format='%(refname:short)' | grep @); do git branch -D $p; done`
)
