package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/josharian/rerunsrv/history"
	"github.com/lithammer/fuzzysearch/fuzzy"
)

var (
	flagHuman         = flag.Bool("human", false, "human at the wheel: stdin reads plain text queries")
	flagCaseSensitive = flag.Bool("case-sensitive", false, "case sensitive search, only used with -human")
	flagMaxResponses  = flag.Int("max", 10, "maximum number of responses to return, only used with -human")
)

type Request struct {
	Query         string `json:"query"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxResults    int    `json:"max_results"`
}

type Response struct {
	Query   string        `json:"query"`
	Results []string      `json:"results"`
	Elapsed time.Duration `json:"elapsed"`
}

func main() {
	processStart := time.Now()
	flag.Parse()

	hist, err := history.Parse()
	if err != nil {
		log.Fatal(err)
	}
	srv := newServer(hist)

	if *flagHuman {
		fmt.Fprintf(os.Stderr, "loaded %d commands in %v\n", len(hist), time.Since(processStart))
	}

	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		if *flagHuman {
			req := &Request{
				Query:         scan.Text(),
				CaseSensitive: *flagCaseSensitive,
				MaxResults:    *flagMaxResponses,
			}
			resp := srv.handle(req)
			for _, cmd := range resp.Results {
				_, err := fmt.Println("\t", cmd)
				if err != nil {
					log.Fatal(err)
				}
			}
			fmt.Printf("in %v\n", resp.Elapsed)
			continue
		}

		req := new(Request)
		err := json.Unmarshal(scan.Bytes(), req)
		if err != nil {
			log.Fatal(err)
		}

		resp := srv.handle(req)

		buf := bufio.NewWriter(os.Stdout)
		err = json.NewEncoder(buf).Encode(resp)
		if err != nil {
			log.Fatal(err)
		}
		if err := buf.Flush(); err != nil {
			log.Fatal(err)
		}
	}

	err = scan.Err()
	if err != nil && err != io.EOF {
		log.Fatal(err)
	}
}

type Server struct {
	cmds        []string
	lowerCmds   []string            // lowercased cmds, for case-insensitive search
	restoreCase map[string][]string // map from lowercased cmd to original cmds
}

func newServer(hist []history.Command) *Server {
	// Extract, de-dupe, and normalize commands.
	cmds := make([]string, 0, len(hist))
	seen := make(map[string]bool)
	slices.Reverse(hist)
	for _, full := range hist {
		cmd := normalize(full.Command)
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		cmds = append(cmds, cmd)
	}
	lowerCmds := make([]string, len(cmds))
	restoreCase := make(map[string][]string)
	for i, cmd := range cmds {
		lower := strings.ToLower(cmd)
		lowerCmds[i] = lower
		restoreCase[lower] = append(restoreCase[lower], cmd)
	}
	return &Server{cmds: cmds, lowerCmds: lowerCmds, restoreCase: restoreCase}
}

func (srv *Server) handle(req *Request) (resp *Response) {
	start := time.Now()
	results := srv.search(req)
	elapsed := time.Since(start)
	return &Response{
		Query:   req.Query,
		Results: results,
		Elapsed: elapsed,
	}
}

func normalize(cmd string) string {
	return strings.TrimRight(cmd, ";\\ \t\n\r")
}

type internalRequest struct {
	cmds       []string
	query      string
	maxResults int
}

type searcher func(*internalRequest) []string

var searchers = []searcher{recent, prefixMatch, substringMatch, multiPrefixMatch, multiSubstringMatch, anchoredPrefixMatch, fuzzyMatch}

func (srv *Server) search(req *Request) []string {
	ir := &internalRequest{
		cmds:       srv.cmds,
		query:      req.Query,
		maxResults: req.MaxResults,
	}
	if !req.CaseSensitive {
		ir.cmds = srv.lowerCmds
		ir.query = strings.ToLower(ir.query)
	}

	var results []string
	seen := make(map[string]bool)
	for _, fn := range searchers {
		// It is tempting to reduce ir.maxResults here if we already have some results.
		// But the newly returned results might be duplicates of ones we already have.
		got := fn(ir)
		for _, out := range got {
			cmds := []string{out}
			if !req.CaseSensitive {
				cmds = srv.restoreCase[out]
				if len(cmds) == 0 {
					panic("internal error: missing restore case for " + out)
				}
			}
			for _, cmd := range cmds {
				if seen[cmd] {
					continue
				}
				seen[cmd] = true
				results = append(results, cmd)
			}
		}
		if len(results) >= req.MaxResults {
			return results[:req.MaxResults]
		}
	}

	return results
}

func recent(req *internalRequest) []string {
	if req.query != "" {
		return nil
	}
	if len(req.cmds) > req.maxResults {
		req.cmds = req.cmds[:req.maxResults]
	}
	return req.cmds
}

var eliminateSpaces = strings.NewReplacer(" ", "")

func fuzzyMatch(req *internalRequest) []string {
	// drop all spaces from the query; they serve no purpose now
	query := eliminateSpaces.Replace(req.query)
	matches := fuzzy.RankFind(query, req.cmds)
	slices.SortStableFunc(matches, func(x, y fuzzy.Rank) int {
		// minor differences in distance are not interesting
		const discount = 64
		xDist := x.Distance / discount
		yDist := y.Distance / discount
		if xDist != yDist {
			return xDist - yDist
		}
		// prefer more recent commands
		return x.OriginalIndex - y.OriginalIndex
	})
	if len(matches) > req.maxResults {
		matches = matches[:req.maxResults]
	}
	results := make([]string, len(matches))
	for i, cmd := range matches {
		results[i] = cmd.Target
	}
	return results
}

func prefixMatch(req *internalRequest) []string {
	return singleMatch(req, strings.HasPrefix)
}

func substringMatch(req *internalRequest) []string {
	return singleMatch(req, strings.Contains)
}

func singleMatch(req *internalRequest, fn func(a, b string) bool) []string {
	results := make([]string, 0, min(len(req.cmds), req.maxResults))
	for _, cmd := range req.cmds {
		if fn(cmd, req.query) {
			results = append(results, cmd)
			if len(results) >= req.maxResults {
				break
			}
		}
	}
	return results
}

func multiPrefixMatch(req *internalRequest) []string {
	words := strings.Fields(req.query)
	return multiMatch(req, words, strings.HasPrefix)
}

func multiSubstringMatch(req *internalRequest) []string {
	words := strings.Fields(req.query)
	return multiMatch(req, words, strings.Contains)
}

func anchoredPrefixMatch(req *internalRequest) []string {
	letters := make([]string, 0, len(req.query))
	for _, r := range req.query {
		letters = append(letters, string(r))
	}
	return multiMatch(req, letters, strings.HasPrefix)
}

func multiMatch(req *internalRequest, needles []string, fn func(a, b string) bool) []string {
	results := make([]string, 0, min(len(req.cmds), req.maxResults))
	for _, cmd := range req.cmds {
		parts := strings.Fields(cmd)
		if matchSlices(parts, needles, fn) {
			results = append(results, cmd)
			if len(results) >= req.maxResults {
				break
			}
		}
	}
	return results
}

// matchSlices reports whether there is a subset of haystack of length len(needle) such that fn reports true for each pair of elements.
func matchSlices(haystack, needles []string, fn func(haystack, needle string) bool) bool {
	if len(needles) > len(haystack) {
		return false
	}
	if len(needles) == 0 {
		return true
	}
	i := 0
	for _, needle := range needles {
		for ; i < len(haystack); i++ {
			if fn(haystack[i], needle) {
				break
			}
		}
		if i == len(haystack) {
			return false
		}
		i++
	}
	return true
}
