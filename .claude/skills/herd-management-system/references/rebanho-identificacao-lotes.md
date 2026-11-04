# Rebanho, identificação, categorias e lotes

## Identidade do animal
- **ID interno estável** (UUID v7). Brinco nunca é chave primária e nunca é sobrescrito sem histórico.
- **Identificadores N:1 tipados**: `{tipo, número}` com tipos como brinco visual, brinco eletrônico (RFID/FDX-B), tatuagem, marca a fogo, SISBOV, botton. Vários por animal, vigência (de/até) e motivo de troca. Não usar "2 slots fixos".
- Cenários que o modelo deve resolver: brinco perdido e recolocado (novo identificador, mesmo animal), brinco duplicado em fazendas ou lotes (conflito vai a resolução humana), leitura RFID sem cadastro (fluxo "animal desconhecido": cadastrar na hora ou associar), fusão de duplicatas com revisão auditada.
- Cadastro mínimo útil: ID interno, ≥0 identificadores, sexo, raça, nascimento (estimado/confirmado, ou idade em dias quando data desconhecida), origem, finalidade, situação, categoria, observações, foto opcional, proprietário(s).
- **Sexo derivado da categoria** evita inconsistência ("matriz macho"); categoria customizada exige declarar sexo.
- **Multi-proprietário** por animal (parceria, arrendamento, boi de terceiro).
- **Situação**: vivo, morto, abatido, vendido/movido, descartado. `ativo=false` bloqueia participação em qualquer manejo e preserva histórico. Animal fora de situação "vivo" é excluído de seleção de manejo e de relatórios correntes (vaca abatida sai do reprodutivo).
- Genealogia: pai/mãe opcionais com **fonte e confiabilidade**; ancestral externo sem cadastro permitido; árvore preservada em transferência entre fazendas. Aptidão (corte/leite/dupla), "puro de origem" (PO) e peso ao nascer opcionais.

## Raça e categorias
- Raça: lista versionada e restrita; evitar raça inativa por padrão (bloqueia o cadastro).
- Categorias editáveis pelo usuário (bezerro/a, garrote, novilha, boi, vaca, touro, matriz…) com sexo declarado. Mudança de categoria é evento ou regra por idade/peso, nunca edição silenciosa.

## Inventário agregado de transição
Produtor quase nunca tem identificação individual no dia 1. Suportar:
1. **Entrada por quantidade e categoria** ("100 matrizes Nelore") como saldo agregado, rotulado na UI como *quantidade estimada*, nunca como animais individuais artificiais.
2. **Identificação em lote**: raça + categoria + tipo de identificador + número inicial; sistema numera sequencialmente os ainda não identificados (100 sem brinco, inicial 100 → 100 a 199). Modo individual apresenta um a um, salvando e avançando (brincagem em série no curral).
3. Importação de planilha com modelo, relatório de linhas válidas/rejeitadas/ambíguas e exportação do erro. Carga inicial assistida (planilha ou foto do caderno com humano no loop) é serviço que converte.
4. Onboarding progressivo por *cards* de tarefa: começar incompleto e enriquecer.
- Identificação parcial é estado normal até pelo menos 2029 (ver rastreabilidade). Ausência de brinco não é erro.

## Eventos de ciclo de vida (append-only)
Nascimento, aquisição, venda, transferência, morte, descarte, perda — com data efetiva, documento/contraparte opcional e motivo. Baixa exige confirmação e deixa rastro. Aquisição pode alimentar custo inicial do animal.

## Lotes e movimentação
- Lote = agrupamento operacional (categoria, objetivo, área). Animal entra/sai com data; histórico de movimentação de animal entre lotes e de lote entre áreas/piquetes é append-only (origem, destino, data, executor, justificativa).
- Manejo pode ser executado por animal, por lote ou por área. Lote tem data de entrada na área e taxa/percentual de ocupação.
- Movimento entre fazendas preserva genealogia e histórico; separar saldo por fazenda.
- Contador de animais (contagem sem individualização) existe, mas não vira cadastro individual.

## Busca e relatórios do rebanho
Busca por identificador (inclusive leitura RFID em qualquer tela com dado de animal), lote, categoria, situação; ~20 filtros combináveis são esperados. Exportação (CSV/PDF) só de registros autorizados, com data e origem.

## Campos customizados
Tipados, validados, versionados, sem alterar semântica de campos canônicos (P1).

## Armadilhas
- Brinco como PK ou como único identificador.
- Saldo agregado exibido como se fossem animais.
- Editar categoria/lote sem evento.
- Ignorar animal sem número (lista deve exibi-lo no topo e filtrá-lo).
- Soft delete indiscriminado em eventos.
