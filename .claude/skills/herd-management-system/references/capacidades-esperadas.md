# Catálogo de capacidades esperadas de um sistema de gestão de gado de corte

Lista consolidada do que um sistema completo oferece. Cada item é capacidade do produto, com fase sugerida (MVP / P1 / P2 / F4). Detalhes de regra ficam nas references por área. Escopo explícito do produto (bovino de corte) vale sobre este catálogo: ordenha e lavoura não entram.

## Legenda de fases
MVP = F1 · P1 = F2 (ou fim de F1 se o piloto exigir) · P2 = F3 · F4 = inteligência. Onde um item traz "MVP/P1", a parte básica é F1 e a ampliação é F2. Decisões pendentes (financeiro básico no MVP, API em todos os planos, bubalino) estão em produto-visao-escopo.

## Diferenciais prioritários (ordem sugerida de investimento)
Nenhum deles é afirmado como exclusivo ou inédito no mercado sem auditoria.
1. Offline-first com sync idempotente, sem perda silenciosa, e mobile que também edita.
2. Indicadores auditáveis (fórmula, janela, fonte, atualização), começando por GMD explicável.
3. Carência com bloqueio e override auditado + aprovação de dado + permissão granular, no mesmo produto.
4. Onboarding para quem ainda não tem identificação individual (saldo agregado → individual) e carga assistida.
5. Hardware testado fisicamente em Android e iOS, com matriz pública de equipamentos.
6. Construtor de manejos por composição de ações, versionado e executável offline, com tarefas e trilha de responsabilidade (P1/F2).
7. Financeiro integrado ao manejo e ao estoque (DRE, centro de custo, rateio, conciliação OFX) e confinamento com MS e conversão.
8. GTA assistida (P1/F2) com conectores oficiais por UF (P2/F3); API aberta (P2/F3); canal de consultor (F3); plano e preço públicos, sem taxa de setup (desde o lançamento).

## Cadastro e rebanho
- Animal individual e saldo por lote/categoria; N identificadores tipados (brinco visual, eletrônico, tatuagem, marca a fogo, SISBOV, botton); ~20 filtros de busca; foto na ficha; multi-proprietário; genealogia com avós externos; aptidão e PO; árvore preservada ao transferir de fazenda. (MVP)
- Onboarding por cards de tarefa, entrada por quantidade+categoria, identificação em lote sequencial, importação de planilha, carga inicial assistida (planilha ou foto de caderno), migração de dados. (MVP)
- Espécie/contexto como dimensão do modelo, extensível (apenas bovino de corte ativo). (MVP)
- Cadastro de máquinas e veículos, instalações, contatos, funcionários, parceiros, fornecedores. (P1)

## Campo e curral
- App offline com sync idempotente, fila visível, aprovação opcional pelo gerente, tarefas por pessoa/equipe com trilha de responsabilidade, manejos pré-configurados, construtor de manejos por composição de ações com versão, calendário de manejo, vários manejos na mesma lida, apartação planejada, registro por GPS, navegação guiada, mapa interativo com lançamento pelo mapa, registro de ocorrência por foto/vídeo/áudio. (MVP/P1)
- Coletas de escore personalizadas (pasto, altura, corporal, cocho, umbigo, cerca elétrica). (P1)
- Relatórios e consulta completa do animal no celular, com edição. (MVP)

## Pesagem e desempenho
- Pesagem individual/lote, rápida e completa, GMD explicável, curvas com GMD sobreposto, previsão de abate, peso ajustado, comparação com melhores desempenhos, importação de pesagens, pesagem em compra/venda. (MVP/P1)
- Projeção de ganho de peso por modelo preditivo e simulador de vendas por kg e por @. (P2/F4)

## Reprodução
- Estação de monta, IA, IATF, monta natural/induzida, FIV, TE, touro múltiplo, implante/retirada, DG (palpação/ultrassom), parto, aborto, desmame, descarte reprodutivo, protocolo hormonal cadastrável com biblioteca de modelos, lotes de IATF com alertas por app/e-mail, avaliação andrológica e de touro, comparação IATF × monta × IA, status reprodutivo (rufião, dúvida em toque), mortalidade por causa, relatórios de previsão de parto e desempenho reprodutivo. (MVP/P1; FIV/TE P2)
- Estoque biológico (botijão, caneca, dose, embrião) com eficácia por sêmen/touro/partida (taxa de prenhez por dose). Protocolo hormonal cadastrável com biblioteca de modelos. (P1)
- Técnico de reprodução multi-cliente: agenda por cliente/fazenda/grupo e relatórios técnicos. (P1)

## Sanidade
- Vacina, vermifugação, tratamento, exame, castração; calendário e protocolos configuráveis; alertas de prazo; baixa de estoque; mapa sanitário; **carência com liberação por animal e bloqueio de venda/abate**; prontuário individual; conclusão em lote offline. (MVP)

## Pastagem
- Piquetes com geometria, mapa com satélite, ocupação e descanso, lotação por UA configurável, pastejo rotacionado, monitoramento de pasto, pluviometria e relatório de chuvas, clima de fonte oficial. (MVP/P1/P2)

## Confinamento
- Baias/linhas/ruas, abertura e fechamento de lote, dieta com MS, trato manual/automatizado, batidas, leitura de cocho, planejado × realizado, consumo, conversão, projeção, painel de balança de vagão, relatórios nutricionais e financeiros do confinamento. (P1)

## Nutrição e estoque
- Estoque de farmácia com baixa por aplicação (MVP, movimento mínimo); estoques nutricional e biológico, ledger completo (P1/F2); validade; mínimo; custo médio recalculável; formulação de ração e controle de insumos; dieta por lote ou por animal; conferência real × planejado; relatório de campo para tratador; multi-almoxarifado e contagem física. (MVP/P1)

## Financeiro e custos
- Contas, pagar/receber, fluxo de caixa, DRE, centros de custo, plano de contas, subcategoria, transferências, rateios dinâmicos, custo por animal/lote/@/cabeça/ha, margem, alertas de vencimento, importação de extrato (OFX) com conciliação, compra/venda de animais com comprovante, razão. (MVP básico/P1)
- Fiscal: NF-e, MDF-e, importação de NF-e por SEFAZ/XML/chave, LCDPR, apuração de IR, livro caixa. (P2/F3)
- BPO financeiro e serviço de lançamento. (P2)
- Máquinas: manutenção preventiva/corretiva, ordem de serviço, horímetro/hodômetro, abastecimento, depreciação; imobilizados. (P1/P2)
- Pessoas: admissão/demissão, ocorrências, horas (se escopo aprovado). (P2)

## Genética
- Importar avaliações, enviar dados a programas (ANCP, ABCZ, outros), histórico de envios, seleção de matrizes/touros, descarte por índices, relatórios para associações, premiações. (P2)

## Abate e comercialização
- Planejar venda/abate, retorno do frigorífico, rendimento, classificação, bonificação/desconto, integração por contrato, cooperativas como canal de dados. (P1/P2)

## Rastreabilidade e regulatório
- SISBOV (anexos), PNIB (identificação parcial, gatilho de brucelose), GTA e SISBOV em modo assistido (checklist + PDF pré-preenchido, sem emissão oficial) em P1/F2; conectores oficiais de GTA por UF, SISBOV oficial e fiscal homologado em P2/F3 (fase única: F3); rastreabilidade como subproduto da gestão, camada de imutabilidade opcional (assinatura/hash/blockchain) sobre eventos, LGPD por categoria (F0/F1).

## Inteligência
- Alertas automáticos por regras e gatilhos; assistente de voz offline com revisão (entrada); consulta em linguagem natural por texto/áudio (saída); relatórios por mensageria (WhatsApp); diagnóstico de maturidade digital; ML sobre base agregada. (P2/F4)

## Indicadores
- ~50 indicadores econômicos e produtivos, 30+ relatórios, personalização, comparação com meta, visões avançadas. (P1)

## Hardware
- Balanças e leitores de múltiplas marcas, homologação nominal pública, homologação sob demanda, auto-homologação, HID, integração com coleiras e sistemas de ordenha apenas se o escopo mudar. (MVP um equipamento; equipamentos adicionais com fixture em P2/F3)

## Plataforma e acesso
- Papéis e permissões por funcionário, multi-fazenda consolidado, consultor multi-propriedade (alternar entre fazendas P1/F2; portal e canal F3), convite, API pública com webhooks (P2/F3), loja de apps/extensões via API para automação IoT, status page, central de ajuda pública, treinamento, suporte com janela estendida e SLA, preços e planos públicos sem taxa de setup (decisão comercial, vale desde o lançamento, não depende de fase de produto), checkout self-service. (P1/P2)
