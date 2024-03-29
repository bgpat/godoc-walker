package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/go-github/v53/github"
	"golang.org/x/oauth2"
)

var (
	redisClient  *redis.Client
	githubClient *github.Client

	redisURL        string
	defaultRedisURL = "redis://localhost:6379"

	githubAccessToken  string
	githubUser         string
	githubOrganization string

	defaultGodocURL     = "http://godoc.org"
	godocURL            *url.URL
	godocRequestTimeout time.Duration

	retryCount = 5
)

func main() {
	godocURLStr := os.Getenv("GODOC_URL")
	if godocURLStr == "" {
		godocURLStr = defaultGodocURL
	}
	var err error
	godocURL, err = url.Parse(godocURLStr)
	if err != nil {
		log.Fatalf("failed to parse $GODOC_URL: %v", err)
	}
	log.Println("godocURL =", godocURL)

	if godocReqTimeoutStr := os.Getenv("GODOC_REQUEST_TIMEOUT"); godocReqTimeoutStr != "" {
		godocRequestTimeout, err = time.ParseDuration(godocReqTimeoutStr)
		if err != nil {
			log.Fatalf("failed to parse $GODOC_REQUEST_TIMEOUT: %v", err)
		}
		log.Println("godocRequestTimeout =", godocRequestTimeout)
	}

	if retryCountStr := os.Getenv("RETRY_COUNT"); retryCountStr != "" {
		retryCount, err = strconv.Atoi(retryCountStr)
		if err != nil {
			log.Fatalf("failed to parse $RETRY_COUNT: %v", err)
		}
	}

	var (
		pkgs []string
		repo string
	)
	if len(os.Args) > 1 {
		fmt.Println("")
		pkgs = make([]string, 0)
		for _, repo := range os.Args[1:] {
			repoURL, err := url.Parse(repo)
			if err != nil {
				log.Fatalf("failed to parse URL '%s': %v", repo, err)
			}
			p, err := getPackages(*repoURL)
			if err != nil {
				log.Fatalf("failed to get pkg list: %v", err)
			}
			log.Println("repository:", repo)
			pkgs = append(pkgs, p...)
		}
	} else {
		if err := initRedis(); err != nil {
			log.Fatalf("failed to initialize Redis client: %v", err)
		}

		if err := initGitHub(); err != nil {
			log.Fatalf("failed to initialize GitHub client: %v", err)
		}

		githubOrganization = os.Getenv("GITHUB_ORGANIZATION")

		userInfo, _, err := githubClient.Users.Get(context.Background(), "")
		if err != nil {
			log.Fatalf("failed to get user info: %v", err)
		}
		githubUser = userInfo.GetLogin()

		if githubOrganization == "" {
			log.Println("githubUser =", githubUser)
		} else {
			log.Println("githubOrganization =", githubOrganization)
		}

		repo, err = redisClient.RandomKey().Result()
		if err != nil {
			log.Println("repository queue is empty:", err.Error())

			repos, err := getRepositories()
			if err != nil {
				log.Fatalf("failed to get repository list: %v", err)
			}

			pairs := make([]string, 0, len(repos)*2)
			for _, repo := range repos {
				pairs = append(pairs, repo.GetCloneURL(), "0")
			}
			if err := redisClient.MSet(pairs).Err(); err != nil {
				log.Fatalf("failed to store repository list: %v", err)
			}
			return
		}

		fmt.Println("")
		log.Println("repository:", repo)

		if count, err := redisClient.Get(repo).Int(); err != nil {
			log.Println(err)
		} else if count > retryCount {
			log.Printf("skip %q: too many retried\n", repo)
			if err := redisClient.Del(repo); err != nil {
				log.Fatalf("failed to delete key '%s': %v", repo, err)
			}
		}

		repoURL, err := url.Parse(repo)
		if err != nil {
			if err := redisClient.Incr(repo).Err(); err != nil {
				log.Println(err)
			}
			log.Fatalf("failed to parse '%s': %v", repo, err)
		}
		repoURL.User = url.UserPassword(githubUser, githubAccessToken)

		pkgs, err = getPackages(*repoURL)
		if err != nil {
			if err := redisClient.Incr(repo).Err(); err != nil {
				log.Println(err)
			}
			log.Fatalf("failed to get pkg list: %v", err)
		}
	}

	for _, pkg := range pkgs {
		log.Println("package:", pkg)
		var err error
		for i := 0; i < retryCount; i++ {
			err = sync(pkg)
			if err != nil {
				log.Printf("retry to sync %s: %v", pkg, err)
				continue
			}
			break
		}
		if err != nil {
			if err := redisClient.Incr(repo).Err(); err != nil {
				log.Println(err)
			}
			log.Fatalf("failed to sync %s: %v", pkg, err)
		}
	}

	if repo != "" {
		if err := redisClient.Del(repo).Err(); err != nil {
			log.Fatalf("failed to delete key '%s': %v", repo, err)
		}
	}
}

func initRedis() error {
	redisURL = os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = defaultRedisURL
	}
	log.Println("redisURL =", redisURL)

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return err
	}

	redisClient = redis.NewClient(opt)
	return nil
}

func initGitHub() error {
	githubAccessToken = os.Getenv("GITHUB_ACCESS_TOKEN")
	if githubAccessToken == "" {
		githubAccessToken = os.Getenv("GITHUB_TOKEN")
	}
	if githubAccessToken == "" {
		return errors.New("$GITHUB_ACCESS_TOKEN is required")
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubAccessToken})
	tc := oauth2.NewClient(ctx, ts)

	githubClient = github.NewClient(tc)
	return nil
}

func getRepositories() ([]*github.Repository, error) {
	pagination := github.ListOptions{PerPage: 100}

	var allRepos []*github.Repository
	for {
		var (
			repos []*github.Repository
			resp  *github.Response
			err   error
		)
		if githubOrganization == "" {
			repos, resp, err = githubClient.Repositories.List(
				context.Background(),
				githubUser,
				&github.RepositoryListOptions{
					ListOptions: pagination,
				},
			)
		} else {
			repos, resp, err = githubClient.Repositories.ListByOrg(
				context.Background(),
				githubOrganization,
				&github.RepositoryListByOrgOptions{
					ListOptions: pagination,
				},
			)
		}
		if err != nil {
			return allRepos, err
		}

		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		pagination.Page = resp.NextPage
	}

	return allRepos, nil
}

func gitClone(repo, dir string) error {
	cmd := exec.Command("git", "clone", repo, dir, "--depth=1")
	return cmd.Run()
}

func goList(pkg, gopath string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("go", "list", filepath.Join(pkg, "..."))
	cmd.Env = append(os.Environ(), "GOPATH="+gopath)
	cmd.Dir = gopath
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	cmd.Run()
	return buf.String(), nil
}

func getPackages(repoURL url.URL) ([]string, error) {
	tempDir, err := ioutil.TempDir("", "godoc-walker")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	pkgPath := filepath.Join(repoURL.Host, strings.TrimSuffix(repoURL.Path, ".git"))
	cloneDir := filepath.Join(tempDir, "src", pkgPath)
	if err := gitClone(repoURL.String(), cloneDir); err != nil {
		return nil, err
	}

	pkgs, err := goList(pkgPath, tempDir)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(pkgs)
	if s == "" {
		return []string{}, nil
	}
	return strings.Split(s, "\n"), nil
}

func sync(pkg string) error {
	values := url.Values{}
	values.Set("path", pkg)

	refreshURL := *godocURL
	refreshURL.Path = path.Join(refreshURL.Path, "-/refresh")
	req, err := http.NewRequest("POST", refreshURL.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	if godocRequestTimeout != 0 {
		client.Transport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   godocRequestTimeout,
				DualStack: true,
			}).DialContext,
			IdleConnTimeout:       godocRequestTimeout,
			TLSHandshakeTimeout:   godocRequestTimeout,
			ExpectContinueTimeout: godocRequestTimeout,
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == 404 {
		fmt.Fprintf(os.Stderr, "%v returns status code as 404", godocURL)
	} else if resp.StatusCode >= 400 {
		return fmt.Errorf("%v returns status code as %d", godocURL, resp.StatusCode)
	}
	return nil
}
