package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

type Project struct {
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
}

type Group struct {
	ID       int    `json:"id"`
	FullPath string `json:"full_path"`
}

var (
	gitlabAPIURL string
	privateToken string
	groupID      string
	cloneDir     string
	sslVerify    bool
	httpClient   *http.Client
	stdinScanner = bufio.NewScanner(os.Stdin)
)

func prompt(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	if !stdinScanner.Scan() {
		if err := stdinScanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Ошибка чтения stdin: %v\n", err)
			os.Exit(1)
		}
		return defaultVal
	}
	val := strings.TrimSpace(stdinScanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

func promptSecret(label, currentVal string) string {
	if currentVal != "" {
		fmt.Printf("%s [set, Enter чтобы оставить]: ", label)
	} else {
		fmt.Printf("%s: ", label)
	}
	val, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	typed := strings.TrimSpace(string(val))
	if err != nil || typed == "" {
		return currentVal
	}
	return typed
}

func envWithDefault(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

func configure() {
	defaultCloneDir, _ := os.Getwd()

	gitlabURL := strings.TrimRight(prompt("GitLab URL", envWithDefault("GITLAB_CLONER_URL", "https://gitlab.com")), "/")
	if !strings.HasPrefix(gitlabURL, "http://") && !strings.HasPrefix(gitlabURL, "https://") {
		gitlabURL = "https://" + gitlabURL
	}
	apiPath := strings.TrimRight(prompt("GitLab API path", envWithDefault("GITLAB_CLONER_API_PATH", "/api/v4")), "/")
	gitlabAPIURL = gitlabURL + apiPath + "/"

	privateToken = promptSecret("Token", envWithDefault("GITLAB_CLONER_TOKEN", ""))

	groupID = prompt("Group ID", envWithDefault("GITLAB_CLONER_GROUP_ID", ""))
	sslVerifyStr := prompt("SSL verify (true/false)", envWithDefault("GITLAB_CLONER_SSL_VERIFY", "true"))
	sslVerify = strings.ToLower(sslVerifyStr) != "false"
	cloneDir = prompt("Clone dir", envWithDefault("GITLAB_CLONER_DIR", defaultCloneDir))

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !sslVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient = &http.Client{Transport: transport}
}


func apiGet(rawURL string, params map[string]string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if params != nil {
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Private-Token", privateToken)
	return httpClient.Do(req)
}

func paginate[T any](firstURL string, params map[string]string) ([]T, error) {
	var items []T
	currentURL := firstURL
	currentParams := params
	for currentURL != "" {
		resp, err := apiGet(currentURL, currentParams)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", currentURL, err)
		}
		if resp.StatusCode >= 300 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("GET %s: HTTP %d: %s", currentURL, resp.StatusCode, strings.TrimSpace(string(errBody)))
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var page []T
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		items = append(items, page...)

		currentURL = ""
		currentParams = nil
		if link := resp.Header.Get("Link"); link != "" {
			currentURL = parseNextLink(link)
		}
	}
	return items, nil
}

func parseNextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) != 2 {
			continue
		}
		rel := strings.TrimSpace(segments[1])
		if rel == `rel="next"` {
			u := strings.Trim(strings.TrimSpace(segments[0]), "<>")
			return u
		}
	}
	return ""
}

func getGroupInfo(id string) (*Group, error) {
	resp, err := apiGet(gitlabAPIURL+"groups/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET groups/%s: HTTP %d: %s", id, resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var g Group
	return &g, json.Unmarshal(body, &g)
}

func getProjects(groupID string) ([]Project, error) {
	return paginate[Project](
		gitlabAPIURL+"groups/"+groupID+"/projects",
		map[string]string{"per_page": "100"},
	)
}

func getSubgroups(groupID string) ([]Group, error) {
	return paginate[Group](
		gitlabAPIURL+"groups/"+groupID+"/subgroups",
		map[string]string{"per_page": "100"},
	)
}

func gitEnv() []string {
	env := os.Environ()
	if !sslVerify {
		env = append(env, "GIT_SSL_NO_VERIFY=true")
	}
	return env
}

func pullRepository(clonePath string) error {
	fmt.Printf("[pull] %s\n", clonePath)
	cmd := exec.Command("git", "-C", clonePath, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = gitEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pull %s: %w", clonePath, err)
	}
	return nil
}

func cloneRepository(repoURL, clonePath string) error {
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
		return pullRepository(clonePath)
	}
	authURL := strings.Replace(repoURL, "https://", fmt.Sprintf("https://oauth2:%s@", privateToken), 1)
	cmd := exec.Command("git", "clone", authURL, clonePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = gitEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone %s: %w", clonePath, err)
	}
	return nil
}

func cloneGroupProjects(groupID, parentDir, accumulatedPath string) error {
	projects, err := getProjects(groupID)
	if err != nil {
		return fmt.Errorf("projects in group %s: %w", groupID, err)
	}
	for _, p := range projects {
		relPath := strings.TrimPrefix(p.PathWithNamespace, accumulatedPath+"/")
		clonePath := filepath.Join(parentDir, relPath)
		if err := os.MkdirAll(clonePath, 0o755); err != nil {
			return err
		}
		if err := cloneRepository(p.HTTPURLToRepo, clonePath); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		}
	}

	subgroups, err := getSubgroups(groupID)
	if err != nil {
		return fmt.Errorf("subgroups of %s: %w", groupID, err)
	}
	for _, sg := range subgroups {
		relPath := strings.TrimPrefix(sg.FullPath, accumulatedPath+"/")
		subDir := filepath.Join(parentDir, relPath)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return err
		}
		if err := cloneGroupProjects(fmt.Sprintf("%d", sg.ID), subDir, sg.FullPath); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	configure()

	if privateToken == "" || groupID == "" {
		fmt.Fprintln(os.Stderr, "PRIVATE_TOKEN и GROUP_ID обязательны")
		os.Exit(1)
	}

	rootGroup, err := getGroupInfo(groupID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка получения группы: %v\n", err)
		os.Exit(1)
	}

	// strip только родительский путь, чтобы имя самой группы осталось в структуре
	parentPath := ""
	if idx := strings.LastIndex(rootGroup.FullPath, "/"); idx != -1 {
		parentPath = rootGroup.FullPath[:idx]
	}

	if err := cloneGroupProjects(groupID, cloneDir, parentPath); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}
