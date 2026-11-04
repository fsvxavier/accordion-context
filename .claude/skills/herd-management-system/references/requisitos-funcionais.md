# Catálogo de requisitos funcionais (resumo por prioridade)

Prioridades: MVP / P1 / P2 / Pendente. Proposta v1 a validar com produtores.

## RF-ORG organização e acesso
MVP: criar organização e fazendas (localização, fuso, áreas, instalações, unidades); convidar/revogar usuários por fazenda; papéis owner/gestor/vet/campo com permissões granulares. P1: resumo consolidado multi-fazenda; consultor alterna entre fazendas autorizadas.

## RF-REB rebanho e inventário
MVP: cadastro com ID interno e N identificadores; lote/categoria e saldo agregado de migração (rotulado como estimativa); pai/mãe com fonte; nascimento, aquisição, venda, transferência, morte, descarte, perda; movimentação append-only; busca/filtros/exportação autorizada. P1: campos customizados tipados. Pendente: bubalino.

## RF-CAM campo e sincronização
MVP: lista de trabalho de curral e download offline com data de sync; pesagem/manejo offline com idempotency key; fila de estados; aprovação opcional do gerente; tarefas por pessoa/equipe com anexos; fusos/relógios divergentes. P1: templates de manejo versionados.

## RF-PES pesagem e desempenho
MVP: pesagem individual/lote com unidade, data, método, operador, origem; GMD explicável; correção como nova versão; matriz pública só de modelos testados. P1: agendar pesagem; peso ajustado (205 d).

## RF-REP reprodução
MVP: estação de monta; cobertura/IA/IATF; diagnóstico com previsão de parto por parâmetro versionado; parto/cria/paternidade conhecida-provável-desconhecida. P1: aborto/perda/desmame/descarte com denominadores; estoque biológico. P2: FIV/TE.

## RF-SAN sanidade
MVP: vacina, tratamento, exame (produto, lote, dose, via, aplicador, vet); protocolos configuráveis; validade/lote e baixa de estoque após confirmação com estorno; **carência por animal/produto com liberação e sinalização em venda/abate**. P1: alertas previstos/atrasados e conclusão em lote offline; requisitos de rastreabilidade por UF.

## RF-PAST pastagens
MVP: piquete (ha, capacidade, localização); movimentar lotes e manter ocupação. P1: lotação com UA configurável. P2: mapas, satélite, NDVI, clima.

## RF-CNF confinamento (P1/P2)
Baias e ciclo; dieta/trato com MS e custo; trato realizado e leitura de cocho; indicadores de consumo, GMD, conversão; P2: projeção de abate/custo como cenário.

## RF-INV estoque
MVP: cadastro de insumo de farmácia e baixa por aplicação sanitária (movimento append-only mínimo, com estorno). P1/F2: ledger completo, alertas de mínimo/validade, nutrição, multi-almoxarifado e conciliação.

## RF-FIN custos e financeiro
MVP (candidato, decisão pendente do dono do produto): lançar receita/despesa (competência/pagamento) e registrar venda e compra de animais; acesso mínimo privilégio. P1/F2: custo por centro/lote/animal com rateio explícito, painel operacional, fluxo de caixa, importar extrato. P2: integração bancária; NF-e/MDF-e/LCDPR após validação.

## RF-ABT abate/venda
P1: planejar seleção; registrar peso vivo/carcaça/classificação/rendimento/descontos. P2: integração com frigorífico via contrato versionado.

## RF-REG rastreabilidade
P1: identificadores, origem, eventos e documentos com cadeia temporal. P2: integração oficial só com autorização e homologação; SISBOV/PNIB e exigências estaduais parametrizados e versionados.

## RF-OUT relatórios, API, notificações
MVP: relatórios básicos. P1: alertas configuráveis; indicador com fonte/janela/atualização. P2: API/webhooks.

## Regras transversais de aceite
Repetição offline não duplica evento nem baixa. Correções preservam ator, hora de negócio, hora de recepção, origem, versão. Nenhum indicador oculta exclusões. Lote mostra resumo antes e depois. Cross-farm bloqueado no banco e testado com identidades independentes. Todo cálculo com unidade e arredondamento definidos, paridade de implementação e casos de borda.
