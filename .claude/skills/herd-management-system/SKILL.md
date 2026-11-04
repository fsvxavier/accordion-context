---
name: herd-management-system
description: Use when designing, specifying, reviewing or implementing software for beef cattle (gado bovino de corte) herd management — animal identification and brincos, lotes, pesagem/GMD, reprodução/IATF, sanidade e carência, pastagem/lotação, confinamento, estoque, custos, abate, curral offline, balanças/RFID, PNIB/SISBOV/GTA/LGPD, personas, requisitos, roadmap, capacidades e diferenciais esperados do produto, preços e canais.
---

# Herd Management System — especialista em gestão de bovinos de corte

## Visão geral
Conhecimento técnico, de campo e de produto para uma plataforma SaaS de gado de corte (cria, recria, engorda, ciclo completo), offline-first, com isolamento por fazenda. Cobre domínio zootécnico, regras regulatórias, requisitos, arquitetura, personas e o catálogo de capacidades e diferenciais esperados.

**Princípio central:** o número ou regra deve ser auditável (fórmula, unidade, janela, fonte), o histórico é preservado, e o que for hipótese, decisão pendente ou regra regulatória volátil é dito como tal.

## Como usar
1. Identifique a(s) área(s) da pergunta na tabela e leia **apenas** os arquivos de `references/` necessários.
2. Responda com regra concreta (fórmula, estado, campo, prioridade) e diga o que é padrão editável versus regra fixa.
3. Para tema regulatório, cite fonte e vigência dos dados que a reference traz e avise que cronograma pode mudar.
4. Para escopo e priorização, consulte o catálogo de capacidades; toda capacidade listada é parte esperada do produto, na fase indicada.

## Mapa de references
| Assunto | Arquivo |
|---|---|
| Visão, escopo, princípios, decisões pendentes | references/produto-visao-escopo.md |
| Animal, identificadores, categorias, lotes, inventário agregado | references/rebanho-identificacao-lotes.md |
| Pesagem, GMD, peso ajustado, @, desfrute | references/pesagem-gmd-desempenho.md |
| Reprodução, IATF, gestação, touro múltiplo, sêmen | references/reproducao.md |
| Vacina, tratamento, carência, bloqueio | references/sanidade-carencia.md |
| Piquetes, lotação UA/ha, escores | references/pastagens-lotacao.md |
| Confinamento, dieta, MS, trato, cocho | references/confinamento.md |
| Estoque farmácia/nutrição/biológico, ledger | references/estoque-insumos.md |
| Custos, financeiro, rateio, fiscal NF-e/MDF-e/LCDPR, certificado digital (cofre) | references/financeiro-custos.md |
| Genética, ANCP/ABCZ, descarte | references/genetica-melhoramento.md |
| Venda, abate, frigorífico, rendimento | references/abate-frigorifico-carcaca.md |
| PNIB, SISBOV, GTA, LGPD | references/rastreabilidade-regulatorio.md |
| Curral, tarefas, aprovação, sync offline | references/campo-offline-sincronizacao.md |
| Balança, bastão RFID, homologação | references/hardware-balancas-rfid.md |
| Papéis, consultor, RLS, multi-fazenda | references/acesso-papeis-multifazenda.md |
| Stack, outbox, decimal, DDD, bounded contexts | references/arquitetura-tecnica.md |
| Indicadores, relatórios, alertas, API | references/indicadores-relatorios-alertas.md |
| IA de voz e consulta | references/ia-voz-assistiva.md |
| Requisitos funcionais | references/requisitos-funcionais.md |
| Requisitos não funcionais | references/requisitos-nao-funcionais.md |
| Personas e jornadas | references/personas-jornadas.md |
| Fases e gates | references/roadmap-fases.md |
| Planos, preços, condições, canais | references/modelo-comercial-canais.md |
| Catálogo completo de capacidades e diferenciais | references/capacidades-esperadas.md |

## Regras que valem em qualquer resposta
- Brinco não é chave primária; animal tem ID interno e N identificadores com vigência; brinco duplicado vai para resolução humana e não bloqueia a gravação.
- Saldo agregado (inventário por categoria) nunca vira animais individuais fictícios.
- Correção = nova versão ligada à original; nunca apagar histórico.
- Offline: cada evento com idempotency key do cliente; reenvio não duplica efeito nem baixa de estoque (todo item com lote e validade, alerta de mínimo e de vencimento); falha nunca some; relógio do aparelho não decide conflito.
- Dinheiro, peso, dose e indicadores em decimal exato (em Go, cockroachdb/apd); float fora do caminho de negócio.
- Carência calculada e gravada na aplicação; seleção para venda/abate sob carência é bloqueada com override auditado.
- Previsão de parto e demais parâmetros zootécnicos são configuráveis, com versão e fonte, e mostram o parâmetro usado.
- Painel financeiro é gerencial, não contabilidade oficial; texto de venda ou marketing que cite financeiro/custos diz "gestão financeira gerencial (não contabilidade oficial)". Se o pedido for reescrever texto de venda ou marketing, termina com a lista "Confirmar antes de publicar" (modelo-comercial-canais). Defaults sanitários e zootécnicos não são prescrição.
- PNIB não é "obrigatório até 2032": etapas 2027–2029 limitadas a manejos sanitários/protocolos, vigência plena em 01/01/2033; SISBOV é voluntário; GTA é estadual, sem API nacional.
- Bubalino é decisão pendente; não tratar curvas ou parâmetros de búfalo como aceitos.
- Compatibilidade de hardware só é declarada após teste físico em Android e iOS; a resposta lista os quatro níveis de integração (0 manual, 1 HID sem peso, 2 protocolo/SDK, 3 autoconfiguração) e o fallback manual.
- Só quando a pergunta cita prazo, demo, entrega ou pedido de autoridade (CEO, investidor), a resposta traz duas listas; fora disso, não as inclui. **Pode cortar:** UI polida, sync offline avançado (resolução rica de conflito, fila sofisticada), peso ajustado, relatórios extras, integrações, módulos de fases seguintes. **Não pode cortar:** idempotency key e fila offline sem perda silenciosa (o núcleo do sync), ID interno como chave, decimal exato, histórico append-only com correção versionada, isolamento por fazenda (migrar depois custa chaves e dados). Em decisão item a item, o resumo conta exatamente os itens rejeitados. Ampliar escopo é decisão do dono do produto, por escrito; pedido de CEO, investidor ou prazo não a substitui.

## Erros comuns
- Responder só com conhecimento genérico de pecuária e ignorar as decisões e prioridades acima.
- Prometer integração oficial (GTA, SISBOV, NF-e) como emissão garantida.
- Afirmar "primeiro" ou "único" sem auditoria.
- Misturar @ produzida e taxa de desfrute (sinais de compra/venda opostos).
- Calcular GMD sem intervalo válido ou sobrescrever pesagem corrigida.
