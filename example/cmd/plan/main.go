package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	graphqlschema "github.com/cevazrem/gql-query-planner/example/api"
	planner "github.com/cevazrem/gql-query-planner/planner"
	"github.com/cevazrem/gql-query-planner/planner/registry"
)

func main() {
	gqlPlanner, err := planner.NewFromFS(graphqlschema.FS, ".")
	if err != nil {
		log.Fatalf("load schema: %v", err)
	}

	resolverRegistry := registry.New()
	registerDemoResolvers(resolverRegistry, newDemoData())

	queryHandler := planner.NewHTTPHandler(
		gqlPlanner,
		resolverRegistry,
		planner.LogConfig{
			LogPlans:         true,
			LogPlanBreakdown: true,
		},
	)

	http.Handle("/query", queryHandler)
	http.HandleFunc("/examples", examplesHandler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})

	log.Println("movie planner demo: POST GraphQL requests to http://localhost:8080/query")
	log.Println("movie planner demo: sample requests are available at http://localhost:8080/examples")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func examplesHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sampleRequests)
}

var sampleRequests = []map[string]any{
	{
		"name":          "MovieCard",
		"operationName": "MovieCard",
		"query": `query MovieCard($id: ID!) {
  movie(id: $id) {
    id
    title
    year
    genre
    rating
    studio { id name country }
    actors(limit: 4) { id name bornYear }
  }
}`,
		"variables": map[string]any{"id": "movie-1"},
	},
	{
		"name":          "CatalogPage",
		"operationName": "CatalogPage",
		"query": `query CatalogPage($input: MovieListInput!) {
  movies(input: $input) {
    totalCount
    pageInfo { hasNextPage endCursor }
    movies {
      id
      title
      year
      studio { id name }
      actors(limit: 3) { id name }
      reviews(limit: 2) {
        id
        rating
        text
        author { id name reputation }
      }
    }
  }
}`,
		"variables": map[string]any{"input": map[string]any{"limit": 6, "genre": "sci-fi", "minYear": 1980}},
	},
	{
		"name":          "StudioGraph",
		"operationName": "StudioGraph",
		"query": `query StudioGraph($id: ID!) {
  studio(id: $id) {
    id
    name
    country
    movies(limit: 5) {
      id
      title
      actors(limit: 3) {
        id
        name
        movies(limit: 2) { id title year }
      }
    }
  }
}`,
		"variables": map[string]any{"id": "studio-1"},
	},
}

type Movie struct {
	ID       string
	Title    string
	Year     int
	Genre    string
	Rating   float64
	StudioID string
	ActorIDs []string
}

type Studio struct {
	ID          string
	Name        string
	Country     string
	FoundedYear int
}

type Actor struct {
	ID       string
	Name     string
	BornYear int
}

type Review struct {
	ID       string
	MovieID  string
	Rating   int
	Text     string
	AuthorID string
}

type Critic struct {
	ID         string
	Name       string
	Reputation int
}

type MoviesPayload struct {
	Movies     []*Movie
	TotalCount int
	PageInfo   *PageInfo
}

type PageInfo struct {
	HasNextPage bool
	EndCursor   string
}

type demoData struct {
	mu  sync.Mutex
	rnd *rand.Rand

	movies  []*Movie
	studios []*Studio
	actors  []*Actor
	reviews []*Review
	critics []*Critic
}

func newDemoData() *demoData {
	return &demoData{
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
		studios: []*Studio{
			{ID: "studio-1", Name: "North Star Pictures", Country: "USA", FoundedYear: 1977},
			{ID: "studio-2", Name: "Mosfilm Nebula", Country: "Russia", FoundedYear: 1924},
			{ID: "studio-3", Name: "Kyoto Frame", Country: "Japan", FoundedYear: 1988},
		},
		actors: []*Actor{
			{ID: "actor-1", Name: "Mira Stone", BornYear: 1988},
			{ID: "actor-2", Name: "Leo Argent", BornYear: 1979},
			{ID: "actor-3", Name: "Nina Volkova", BornYear: 1991},
			{ID: "actor-4", Name: "Ken Sato", BornYear: 1984},
			{ID: "actor-5", Name: "Eva Marin", BornYear: 1996},
			{ID: "actor-6", Name: "Oleg Tumanov", BornYear: 1972},
		},
		movies: []*Movie{
			{ID: "movie-1", Title: "Orbital Dawn", Year: 2017, Genre: "sci-fi", Rating: 8.4, StudioID: "studio-1", ActorIDs: []string{"actor-1", "actor-2", "actor-4"}},
			{ID: "movie-2", Title: "The Last Archive", Year: 2009, Genre: "thriller", Rating: 7.7, StudioID: "studio-2", ActorIDs: []string{"actor-2", "actor-3", "actor-6"}},
			{ID: "movie-3", Title: "Paper Comets", Year: 2021, Genre: "drama", Rating: 8.1, StudioID: "studio-3", ActorIDs: []string{"actor-1", "actor-5"}},
			{ID: "movie-4", Title: "Signal from Europa", Year: 1999, Genre: "sci-fi", Rating: 8.9, StudioID: "studio-1", ActorIDs: []string{"actor-3", "actor-4", "actor-6"}},
			{ID: "movie-5", Title: "Silent Harbor", Year: 2013, Genre: "drama", Rating: 7.9, StudioID: "studio-2", ActorIDs: []string{"actor-2", "actor-5"}},
			{ID: "movie-6", Title: "Neon Pilgrim", Year: 2024, Genre: "sci-fi", Rating: 8.0, StudioID: "studio-3", ActorIDs: []string{"actor-1", "actor-4", "actor-5"}},
			{ID: "movie-7", Title: "Lake of Glass", Year: 1986, Genre: "fantasy", Rating: 7.4, StudioID: "studio-2", ActorIDs: []string{"actor-3", "actor-6"}},
			{ID: "movie-8", Title: "Clockwork Orchard", Year: 2002, Genre: "fantasy", Rating: 7.6, StudioID: "studio-3", ActorIDs: []string{"actor-4", "actor-5"}},
		},
		critics: []*Critic{
			{ID: "critic-1", Name: "Anna Ray", Reputation: 93},
			{ID: "critic-2", Name: "Max Feld", Reputation: 88},
			{ID: "critic-3", Name: "Sergey Lin", Reputation: 79},
		},
		reviews: []*Review{
			{ID: "review-1", MovieID: "movie-1", Rating: 9, Text: "Great pacing", AuthorID: "critic-1"},
			{ID: "review-2", MovieID: "movie-1", Rating: 8, Text: "Strong visual style", AuthorID: "critic-2"},
			{ID: "review-3", MovieID: "movie-2", Rating: 7, Text: "Solid thriller", AuthorID: "critic-3"},
			{ID: "review-4", MovieID: "movie-3", Rating: 8, Text: "Warm and precise", AuthorID: "critic-1"},
			{ID: "review-5", MovieID: "movie-4", Rating: 9, Text: "A classic", AuthorID: "critic-2"},
			{ID: "review-6", MovieID: "movie-4", Rating: 10, Text: "Still impressive", AuthorID: "critic-1"},
			{ID: "review-7", MovieID: "movie-6", Rating: 8, Text: "Ambitious", AuthorID: "critic-3"},
		},
	}
}

func (d *demoData) sleepIO(baseMs, jitterMs int) {
	if baseMs <= 0 && jitterMs <= 0 {
		return
	}
	d.mu.Lock()
	extra := 0
	if jitterMs > 0 {
		extra = d.rnd.Intn(jitterMs + 1)
	}
	d.mu.Unlock()
	time.Sleep(time.Duration(baseMs+extra) * time.Millisecond)
}

func (d *demoData) randomPrefixLen(max int) int {
	if max <= 0 {
		return 0
	}
	d.mu.Lock()
	n := 1 + d.rnd.Intn(max)
	d.mu.Unlock()
	return n
}

func (d *demoData) movieByID(id string) *Movie {
	for _, m := range d.movies {
		if m.ID == id {
			return m
		}
	}
	return d.movies[0]
}

func (d *demoData) studioByID(id string) *Studio {
	for _, s := range d.studios {
		if s.ID == id {
			return s
		}
	}
	return d.studios[0]
}

func (d *demoData) actorByID(id string) *Actor {
	for _, a := range d.actors {
		if a.ID == id {
			return a
		}
	}
	return d.actors[0]
}

func (d *demoData) criticByID(id string) *Critic {
	for _, c := range d.critics {
		if c.ID == id {
			return c
		}
	}
	return d.critics[0]
}

func registerDemoResolvers(r *registry.Registry, data *demoData) {
	r.Register("Query", "movie", registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
		data.sleepIO(4, 4)
		return data.movieByID(stringArg(req.Args, "id", "movie-1")), nil
	}))
	r.Register("Query", "studio", registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
		data.sleepIO(4, 4)
		return data.studioByID(stringArg(req.Args, "id", "studio-1")), nil
	}))
	r.Register("Query", "movies", registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
		data.sleepIO(8, 5)
		input := mapArg(req.Args, "input")
		limit := intArg(input, "limit", 5)
		genre := stringArg(input, "genre", "")
		minYear := intArg(input, "minYear", 0)

		filtered := make([]*Movie, 0, len(data.movies))
		for _, m := range data.movies {
			if genre != "" && m.Genre != genre {
				continue
			}
			if minYear > 0 && m.Year < minYear {
				continue
			}
			filtered = append(filtered, m)
		}
		if limit > len(filtered) {
			limit = len(filtered)
		}
		return &MoviesPayload{
			Movies:     filtered[:limit],
			TotalCount: len(filtered),
			PageInfo: &PageInfo{
				HasNextPage: limit < len(filtered),
				EndCursor:   fmt.Sprintf("movie-cursor-%d", limit),
			},
		}, nil
	}))

	registerStructFields(r, "MoviesPayload", "movies", "totalCount", "pageInfo")
	registerStructFields(r, "PageInfo", "hasNextPage", "endCursor")

	registerStructFields(r, "Movie", "id", "title", "year", "genre", "rating")
	registerStructFields(r, "Studio", "id", "name", "country", "foundedYear")
	registerStructFields(r, "Actor", "id", "name", "bornYear")
	registerStructFields(r, "Review", "id", "rating", "text")
	registerStructFields(r, "Critic", "id", "name", "reputation")

	r.Register("Movie", "studio", registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
		data.sleepIO(3, 3)
		movie, ok := req.Parent.(*Movie)
		if !ok {
			return nil, fmt.Errorf("Movie.studio: unexpected parent %T", req.Parent)
		}
		return data.studioByID(movie.StudioID), nil
	}))

	r.Register("Movie", "actors", registry.BatchableField(
		func(ctx context.Context, req registry.ResolveRequest) (any, error) {
			data.sleepIO(5, 5)
			movie, ok := req.Parent.(*Movie)
			if !ok {
				return nil, fmt.Errorf("Movie.actors: unexpected parent %T", req.Parent)
			}
			return data.actorsForMovie(movie, intArg(req.Args, "limit", 5)), nil
		},
		func(ctx context.Context, req registry.BatchResolveRequest) ([]any, error) {
			data.sleepIO(6, 5)
			out := make([]any, len(req.Parents))
			for i, p := range req.Parents {
				movie, ok := p.(*Movie)
				if !ok {
					return nil, fmt.Errorf("Movie.actors batch: unexpected parent %T", p)
				}
				out[i] = data.actorsForMovie(movie, intArg(req.Args, "limit", 5))
			}
			return out, nil
		},
	))

	r.Register("Movie", "reviews", registry.BatchableField(
		func(ctx context.Context, req registry.ResolveRequest) (any, error) {
			data.sleepIO(5, 5)
			movie, ok := req.Parent.(*Movie)
			if !ok {
				return nil, fmt.Errorf("Movie.reviews: unexpected parent %T", req.Parent)
			}
			return data.reviewsForMovie(movie, intArg(req.Args, "limit", 3)), nil
		},
		func(ctx context.Context, req registry.BatchResolveRequest) ([]any, error) {
			data.sleepIO(7, 5)
			out := make([]any, len(req.Parents))
			for i, p := range req.Parents {
				movie, ok := p.(*Movie)
				if !ok {
					return nil, fmt.Errorf("Movie.reviews batch: unexpected parent %T", p)
				}
				out[i] = data.reviewsForMovie(movie, intArg(req.Args, "limit", 3))
			}
			return out, nil
		},
	))

	r.Register("Studio", "movies", registry.BatchableField(
		func(ctx context.Context, req registry.ResolveRequest) (any, error) {
			data.sleepIO(5, 5)
			studio, ok := req.Parent.(*Studio)
			if !ok {
				return nil, fmt.Errorf("Studio.movies: unexpected parent %T", req.Parent)
			}
			return data.moviesForStudio(studio, intArg(req.Args, "limit", 10)), nil
		},
		func(ctx context.Context, req registry.BatchResolveRequest) ([]any, error) {
			data.sleepIO(7, 5)
			out := make([]any, len(req.Parents))
			for i, p := range req.Parents {
				studio, ok := p.(*Studio)
				if !ok {
					return nil, fmt.Errorf("Studio.movies batch: unexpected parent %T", p)
				}
				out[i] = data.moviesForStudio(studio, intArg(req.Args, "limit", 10))
			}
			return out, nil
		},
	))

	r.Register("Actor", "movies", registry.BatchableField(
		func(ctx context.Context, req registry.ResolveRequest) (any, error) {
			data.sleepIO(5, 5)
			actor, ok := req.Parent.(*Actor)
			if !ok {
				return nil, fmt.Errorf("Actor.movies: unexpected parent %T", req.Parent)
			}
			return data.moviesForActor(actor, intArg(req.Args, "limit", 5)), nil
		},
		func(ctx context.Context, req registry.BatchResolveRequest) ([]any, error) {
			data.sleepIO(7, 5)
			out := make([]any, len(req.Parents))
			for i, p := range req.Parents {
				actor, ok := p.(*Actor)
				if !ok {
					return nil, fmt.Errorf("Actor.movies batch: unexpected parent %T", p)
				}
				out[i] = data.moviesForActor(actor, intArg(req.Args, "limit", 5))
			}
			return out, nil
		},
	))

	r.Register("Review", "author", registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
		data.sleepIO(3, 3)
		review, ok := req.Parent.(*Review)
		if !ok {
			return nil, fmt.Errorf("Review.author: unexpected parent %T", req.Parent)
		}
		return data.criticByID(review.AuthorID), nil
	}))
}

func registerStructFields(r *registry.Registry, parentType string, fieldNames ...string) {
	for _, fieldName := range fieldNames {
		fieldName := fieldName
		r.Register(parentType, fieldName, registry.Field(func(ctx context.Context, req registry.ResolveRequest) (any, error) {
			return getStructField(req.Parent, fieldName)
		}))
	}
}

func (d *demoData) actorsForMovie(movie *Movie, limit int) []*Actor {
	if movie == nil {
		return nil
	}
	if limit <= 0 || limit > len(movie.ActorIDs) {
		limit = len(movie.ActorIDs)
	}
	limit = minInt(limit, d.randomPrefixLen(limit))

	out := make([]*Actor, 0, limit)
	for _, id := range movie.ActorIDs[:limit] {
		out = append(out, d.actorByID(id))
	}
	return out
}

func (d *demoData) reviewsForMovie(movie *Movie, limit int) []*Review {
	if movie == nil {
		return nil
	}
	out := make([]*Review, 0, limit)
	for _, review := range d.reviews {
		if review.MovieID == movie.ID {
			out = append(out, review)
		}
	}
	if limit <= 0 || limit > len(out) {
		limit = len(out)
	}
	if limit > 0 {
		limit = minInt(limit, d.randomPrefixLen(limit))
	}
	return out[:limit]
}

func (d *demoData) moviesForStudio(studio *Studio, limit int) []*Movie {
	if studio == nil {
		return nil
	}
	out := make([]*Movie, 0, limit)
	for _, movie := range d.movies {
		if movie.StudioID == studio.ID {
			out = append(out, movie)
		}
	}
	if limit <= 0 || limit > len(out) {
		limit = len(out)
	}
	if limit > 0 {
		limit = minInt(limit, d.randomPrefixLen(limit))
	}
	return out[:limit]
}

func (d *demoData) moviesForActor(actor *Actor, limit int) []*Movie {
	if actor == nil {
		return nil
	}
	out := make([]*Movie, 0, limit)
	for _, movie := range d.movies {
		for _, actorID := range movie.ActorIDs {
			if actorID == actor.ID {
				out = append(out, movie)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	if limit <= 0 || limit > len(out) {
		limit = len(out)
	}
	if limit > 0 {
		limit = minInt(limit, d.randomPrefixLen(limit))
	}
	return out[:limit]
}

func getStructField(parent any, graphqlField string) (any, error) {
	if parent == nil {
		return nil, nil
	}

	rv := reflect.ValueOf(parent)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("field %q: expected struct parent, got %T", graphqlField, parent)
	}

	for _, name := range goFieldCandidates(graphqlField) {
		fv := rv.FieldByName(name)
		if fv.IsValid() && fv.CanInterface() {
			return fv.Interface(), nil
		}
	}

	return nil, fmt.Errorf("field %q: no matching Go field in %T", graphqlField, parent)
}

func goFieldCandidates(graphqlName string) []string {
	base := upperFirst(graphqlName)
	out := []string{base}

	withID := strings.ReplaceAll(base, "Id", "ID")
	if withID != base {
		out = append(out, withID)
	}
	if strings.EqualFold(graphqlName, "id") {
		out = append(out, "ID", "Id")
	}

	return dedup(out)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func dedup(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func stringArg(args map[string]any, name, fallback string) string {
	if args == nil {
		return fallback
	}
	v, ok := args[name]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return fallback
	}
	return s
}

func intArg(args map[string]any, name string, fallback int) int {
	if args == nil {
		return fallback
	}
	v, ok := args[name]
	if !ok || v == nil {
		return fallback
	}
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		if math.Trunc(x) == x {
			return int(x)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func mapArg(args map[string]any, name string) map[string]any {
	if args == nil {
		return nil
	}
	v, ok := args[name]
	if !ok || v == nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
