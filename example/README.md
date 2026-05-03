# Movie Planner HTTP Example

Минимальный пример подключения вынесенного GraphQL-планировщика как HTTP-handler.

Идея примера:

- схема лежит в `graphqlschema/*.graphql` и подключается через `embed.FS`;
- планировщик создаётся через `planner.NewFromFS(graphqlschema.FS, ".")`;
- резолверы регистрируются вручную через `registry.New()`;
- резолверы являются болванками: возвращают данные из памяти, добавляют небольшой `sleep` и иногда возвращают разный размер списков;
- поля `Movie.actors`, `Movie.reviews`, `Studio.movies`, `Actor.movies` помечены как `@batchable`;
- поля `Movie.studio`, `Review.author` помечены как `@cacheable`.

## Запуск

```bash
go run .
```

Сервер поднимется на:

```text
http://localhost:8080/query
```

Список готовых запросов:

```bash
curl -s http://localhost:8080/examples | jq
```

## Пример запроса

```bash
curl -s http://localhost:8080/query \
  -H 'Content-Type: application/json' \
  -d '{
    "operationName": "CatalogPage",
    "variables": {
      "input": {"limit": 6, "genre": "sci-fi", "minYear": 1980}
    },
    "query": "query CatalogPage($input: MovieListInput!) { movies(input: $input) { totalCount pageInfo { hasNextPage endCursor } movies { id title year studio { id name } actors(limit: 3) { id name } reviews(limit: 2) { id rating text author { id name reputation } } } } }"
  }' | jq
```

В логах должны появиться отладочные сообщения от работы планировщика:

```text
[gqlengine-query2] prepare_us=... plan_us=...
QUERY PLAN
...
[gqlengine-plan-breakdown] build_us=... annotate_us=... optimize_us=... total_us=... logical_nodes=... stats_keys=...
[gqlengine-query2-analyze]
...
[gqlengine-query2-summary] ...
```
