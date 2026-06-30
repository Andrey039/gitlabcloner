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
	"strconv"
	"strings"

	"golang.org/x/term"
)

type Project struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	Archived          bool   `json:"archived"`
}

type Group struct {
	ID       int    `json:"id"`
	FullPath string `json:"full_path"`
}

var (
	gitlabAPIURL          string
	privateToken          string
	groupID               string
	cloneDir              string
	sslVerify             bool
	originProtocol        string
	cloneArchived         bool
	fullClone             bool
	checkoutDefaultBranch bool
	excludeIDs            map[int]struct{}
	httpClient            *http.Client
	stdinScanner          = bufio.NewScanner(os.Stdin)
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

func parseExcludeIDs(s string) map[int]struct{} {
	result := make(map[int]struct{})
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.Atoi(part); err == nil {
			result[id] = struct{}{}
		}
	}
	return result
}

func isExcluded(id int) bool {
	_, ok := excludeIDs[id]
	return ok
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
	cloneArchivedStr := prompt("Clone archived repos (true/false)", envWithDefault("GITLAB_CLONE_ARCHIVED", "false"))
	cloneArchived = strings.ToLower(cloneArchivedStr) == "true"
	fullCloneStr := prompt("Full clone (true/false)", envWithDefault("GITLAB_CLONE_FULL", "false"))
	fullClone = strings.ToLower(fullCloneStr) == "true"
	checkoutDefaultBranchStr := prompt("Checkout default branch before pull (true/false)", envWithDefault("GITLAB_CLONER_CHECKOUT_DEFAULT_BRANCH", "false"))
	checkoutDefaultBranch = strings.ToLower(checkoutDefaultBranchStr) == "true"
	cloneDir = prompt("Clone dir", envWithDefault("GITLAB_CLONER_DIR", defaultCloneDir))
	originProtocol = strings.ToLower(prompt("Origin protocol (ssh/https)", envWithDefault("GITLAB_CLONER_ORIGIN_PROTO", "ssh")))
	excludeIDs = parseExcludeIDs(prompt("Exclude IDs (comma-separated, optional)", envWithDefault("GITLAB_CLONER_EXCLUDE_IDS", "")))

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !sslVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient = &http.Client{Transport: transport}

	fmt.Fprintf(os.Stderr, "[debug] API base URL: %s\n", gitlabAPIURL)
	fmt.Fprintf(os.Stderr, "[debug] Group ID: %s\n", groupID)
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

func checkoutToDefaultBranch(clonePath string) {
	branches := []string{"main", "master", "develop"}
	for _, branch := range branches {
		cmd := exec.Command("git", "-C", clonePath, "checkout", branch)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = gitEnv()
		if err := cmd.Run(); err == nil {
			return
		}
	}
}

func pullRepository(clonePath string) error {
	fmt.Printf("[pull] %s\n", clonePath)
	if checkoutDefaultBranch {
		checkoutToDefaultBranch(clonePath)
	}
	cmd := exec.Command("git", "-C", clonePath, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = gitEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pull %s: %w", clonePath, err)
	}
	return nil
}

func cloneRepository(repoURL, clonePath, originURL string) error {
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
		return pullRepository(clonePath)
	}
	authURL := strings.Replace(repoURL, "https://", fmt.Sprintf("https://oauth2:%s@", privateToken), 1)
	args := []string{"clone"}
	if !fullClone {
		args = append(args, "--depth", "1")
	}
	args = append(args, authURL, clonePath)
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = gitEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone %s: %w", clonePath, err)
	}
	if originURL != "" && originURL != repoURL {
		setURL := exec.Command("git", "-C", clonePath, "remote", "set-url", "origin", originURL)
		setURL.Env = gitEnv()
		if err := setURL.Run(); err != nil {
			return fmt.Errorf("set-url origin %s: %w", clonePath, err)
		}
	}
	return nil
}

func cloneGroupProjects(groupID, parentDir, accumulatedPath string) error {
	projects, err := getProjects(groupID)
	if err != nil {
		errMsg := fmt.Sprintf("%v", err)
		if strings.Contains(errMsg, "404") {
			fmt.Fprintf(os.Stderr, "[warn] projects in group %s: %v\n", groupID, err)
			fmt.Fprintf(os.Stderr, "[hint] Вероятно, у токена нет прав на просмотр проектов (нужен минимум Reporter). Проверьте роль токена в группе %s.\n", groupID)
		} else {
			fmt.Fprintf(os.Stderr, "[warn] projects in group %s: %v — пропускаем\n", groupID, err)
		}
	} else {
		for _, p := range projects {
			if isExcluded(p.ID) {
				fmt.Printf("[skip] project %d (%s)\n", p.ID, p.PathWithNamespace)
				continue
			}
			if !cloneArchived && p.Archived {
				fmt.Printf("[skip] archived project %d (%s)\n", p.ID, p.PathWithNamespace)
				continue
			}
			relPath := strings.TrimPrefix(p.PathWithNamespace, accumulatedPath+"/")
			clonePath := filepath.Join(parentDir, relPath)
			if err := os.MkdirAll(clonePath, 0o755); err != nil {
				return err
			}
			originURL := p.SSHURLToRepo
			if originProtocol == "https" {
				originURL = p.HTTPURLToRepo
			}
			if err := cloneRepository(p.HTTPURLToRepo, clonePath, originURL); err != nil {
				fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			}
		}
	}

	subgroups, err := getSubgroups(groupID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] subgroups of %s: %v — пропускаем\n", groupID, err)
		return nil
	}
	for _, sg := range subgroups {
		if isExcluded(sg.ID) {
			fmt.Printf("[skip] group %d (%s)\n", sg.ID, sg.FullPath)
			continue
		}
		relPath := strings.TrimPrefix(sg.FullPath, accumulatedPath+"/")
		subDir := filepath.Join(parentDir, relPath)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return err
		}
		if err := cloneGroupProjects(fmt.Sprintf("%d", sg.ID), subDir, sg.FullPath); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] group %d: %v — пропускаем\n", sg.ID, err)
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

	fmt.Fprintf(os.Stderr, "[info] Проверяем доступ к группе %s...\n", groupID)

	rootGroup, err := getGroupInfo(groupID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка получения группы: %v\n", err)
		if strings.Contains(fmt.Sprintf("%v", err), "404") {
			fmt.Fprintf(os.Stderr, "[hint] 404 на /groups/%s — проверьте:\n", groupID)
			fmt.Fprintf(os.Stderr, "  1) Правильность Group ID (в URL группы: https://<gitlab>/<group-path>, ID виден в Settings → General)\n")
			fmt.Fprintf(os.Stderr, "  2) Что токен имеет доступ к этой группе (минимум Reporter)\n")
			fmt.Fprintf(os.Stderr, "  3) Что URL (%s) и API path корректны\n", gitlabAPIURL)
		}
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[info] Группа найдена: %s (ID: %d)\n", rootGroup.FullPath, rootGroup.ID)

	testProjects, testErr := getProjects(groupID)
	if testErr != nil {
		fmt.Fprintf(os.Stderr, "[warn] Не удалось получить список проектов: %v\n", testErr)
		if strings.Contains(fmt.Sprintf("%v", testErr), "404") {
			fmt.Fprintf(os.Stderr, "[hint] 404 на /groups/%s/projects при рабочем /groups/%s обычно означает:\n", groupID, groupID)
			fmt.Fprintf(os.Stderr, "  → У токена нет прав на просмотр проектов. Нужна роль минимум Reporter в группе.\n")
			fmt.Fprintf(os.Stderr, "  → Если проекты находятся только в подгруппах, это может быть нормально.\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "[info] Найдено %d прямых проектов в группе\n", len(testProjects))
	}

	testSubgroups, testSubErr := getSubgroups(groupID)
	if testSubErr != nil {
		fmt.Fprintf(os.Stderr, "[warn] Не удалось получить подгруппы: %v\n", testSubErr)
	} else {
		fmt.Fprintf(os.Stderr, "[info] Найдено %d подгрупп\n", len(testSubgroups))
	}

	fmt.Fprintf(os.Stderr, "[info] Начинаем клонирование...\n\n")

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
