# Arquitetura técnica proposta

Tudo é **proposta a validar com spike**, não compromisso aprovado.

## Stack
Backend Go, monólito modular event-driven hexagonal/DDD; PostgreSQL 18 (schema único, RLS, particionamento declarativo, UUID v7 nativo); RabbitMQ via outbox transacional; SPA React/TypeScript; app nativo Android/iOS offline-first; Vitest + Testing Library, Playwright (+axe WCAG AA); mutation testing nightly (go-gremlins) em cálculos críticos; logging estruturado + tracing.

## Decisões centrais
- **Monólito modular** com outbox transacional; consumidores idempotentes por `event_id`, dead-letter e reprocessamento seguro.
- **Zero float** em caminho de negócio: decimal de precisão arbitrária (`cockroachdb/apd`). Peso, moeda, dose, percentual, consumo e indicadores têm tipo/unidade/escala definidos; `NUMERIC(15,2)`/`NUMERIC(8,3)` são ponto de partida, não prova. Payload serializa decimal sem perda (string decimal).
- **Animal como aggregate root**, eventos imutáveis append-only, auditoria híbrida, materialized views com latência declarada.
- **IDs**: UUID v7; id externo (brinco) é chave natural secundária.
- **Conectores de integração**: YAML declarativo (REST simples) + nativo Go (SOAP/stateful) — GTA por UF, SISBOV, banco, frigorífico. Parsers (NF-e, planilha, XML) isolados em pacote próprio sem regra de negócio.
- **Estrutura**: `cmd/`, `infrastructure/` (DDL numerado, seed, samples), `internal/core/<contexto>/{domain,ports,usecases}`, `internal/adapter/{input,output}`, `internal/di`, `pkg/`. DI manual (repositories → usecases → container). Domínio não depende de adapters/banco/transporte.
- **Relay**: escrita grava `outbox_events` na mesma transação + `pg_notify`; relay faz LISTEN, publica e marca `published_at`; varredura periódica reprocessa perdidos (at-least-once).
- **Particionamento** por `LIST(farm_id)` exige spike (FKs, criação, nº de fazendas); decidir por volume medido.
- Migrações reversíveis quando possível, testadas com volume representativo; backup automatizado e **restauração testada**; RPO/RTO com cliente.

## Bounded contexts e fase
iam/farms F0-1; animals F1; weighings F1; reproduction F1-2; health F1; finance F2; pasture F2; inventory F1-2; machinery F3; confinement F3 (produto: F2); genetics F3; weather/satellite F2-3; slaughter F2; notifications F1; audit F0 transversal; mode_config F0; saas billing F1; ai F4; integration (API pública, webhooks, SISBOV, GTA) F2-3.

## Operação
Health/readiness, timeout e shutdown controlado em APIs e workers; circuit breaker/limite de taxa em APIs externas; logs sem PII; UI mostra offline/indisponibilidade/estado de sync, nunca erro como sucesso.

## Qualidade
Cobertura medida por pacote mas não substitui invariantes; cálculos críticos com mutation/property testing e revisão técnica; integração com Postgres e RLS reais; E2E cobre offline, retry, dupla submissão, autorização, troca de fazenda, lote grande, falha de hardware; acessibilidade em fluxos de campo.
