# Visão, escopo e princípios do produto

Plataforma SaaS para **gado bovino de corte**: cria, recria, engorda e ciclo completo. Une manejo de campo offline, gestão zootécnica e controle econômico gradual. Prioridade: utilidade do vaqueiro no curral e visibilidade confiável para gerente e proprietário.

## Escopo
- **Dentro:** rebanho e lotes, campo/manejos, pesagem e desempenho, reprodução, sanidade, pastagens, confinamento, estoque e compras, custos e financeiro operacional, genética e abate, rastreabilidade/integrações, indicadores/relatórios/API.
- **Fora do compromisso inicial:** pecuária leiteira e ordenha; agricultura como ERP geral; marketplace, crédito, seguro; IA que grava dado sem confirmação; emissão automática de GTA, NF-e, MDF-e ou declarações fiscais sem validação por estado, autoridade e contador.
- **Bubalinos:** não confirmados. Arquitetura deixa espécie extensível, mas nenhuma curva, categoria ou gestação de búfalo é tratada como aceita até decisão explícita do dono do produto. Se a decisão for incluir: gestação (parâmetro configurável, versionado e sem valor default), categorias e curvas próprias, cada parâmetro com fonte e aprovação do dono do produto — especialização real, nunca bovino renomeado. Sem fonte citada, não confirmar nem validar nenhum número de búfalo (nem a ordem de grandeza da gestação) e não oferecer default.

## Quando o pedido contradiz o escopo
Sequência: (1) dizer qual item do escopo ou fase o pedido viola; (2) tratar a ampliação como **decisão do dono do produto**, registrada por escrito, nunca do implementador nem decidida sob prazo; (3) se aprovada, vira bounded context novo com discovery (F0): entrevistas, fórmulas, unidades, fontes; (4) enquanto isso, entregar o que é seguro: modelo extensível (espécie/atividade como dimensão) e parâmetros configuráveis (gestação = parâmetro por espécie, versionado, com fonte, sem default e sem curva), sem inventar curva, categoria ou regra; (5) se a ampliação for aprovada, reaproveitar a base comum (animal, eventos offline, decimal exato, isolamento por fazenda, auditoria) e tratar como novo só as regras de domínio. Leite/ordenha e lavoura estão fora do compromisso inicial; bubalino está pendente de decisão.

## Mínimo aceitável sob prazo (demo/MVP)
Pode cortar: UI polida, sync offline avançado (resolução rica de conflito, fila sofisticada), peso ajustado, relatórios extras, integrações. **Não pode cortar** (custo de migração depois): idempotency key e fila offline sem perda silenciosa, ID interno como PK com identificadores à parte, decimal exato para peso e dinheiro, histórico append-only com correção versionada de pesagem, isolamento por fazenda. Cada um custa poucas linhas hoje e uma migração de chaves e dados depois.

## Problema
Fazendas registram em cadernos, planilhas e apps isolados. Identidade, peso, reprodução, sanidade, estoque e custo perdem contexto entre curral e escritório. Resultado: retrabalho, indicador atrasado, decisão com dado incompleto. Tratar como hipótese a validar em entrevistas e pilotos.

## Proposta de valor
- **Campo:** registrar animal/lote e manejo em poucos passos, offline, com hardware realmente testado.
- **Gestão:** histórico rastreável; lotação, peso, reprodução, sanidade, tarefas, estoque e custo ligados a animal/lote.
- **Decisão:** todo indicador com fórmula, unidade, janela, origem dos dados e limitações. Sem "número mágico".
- **Expansão:** integrações por interface versionada; fiscal/regulatório só após validação jurídica, técnica e comercial.

## Princípios inegociáveis
1. Offline-first no curral: fila local durável, sincronização observável, nenhuma perda silenciosa.
2. Animal tem identidade interna estável; brincos são identificadores que podem mudar ou coexistir.
3. Eventos operacionais auditáveis; correção preserva histórico (antes/depois, motivo, ator).
4. Sem precisão numérica implícita: unidade, escala e arredondamento documentados.
5. Segurança por organização/fazenda e menor privilégio.
6. Defaults ajudam; nunca substituem orientação veterinária, zootécnica, fiscal ou regulatória.
7. Construir primeiro só o que o produtor piloto usa e consegue manter atualizado.

## Proposta de diferenciação (conhecimento do produto)
Oportunidade: fluxo bovino de corte coeso, confiável e utilizável offline, com campo, produção e custos conectados. Diferenciais a entregar, cada um detalhado em sua área: indicadores auditáveis (fórmula, janela, fonte); aprovação de dado com permissão granular e carência com bloqueio no mesmo produto; onboarding para quem ainda não tem identificação individual; edição completa no celular; iOS e Android com hardware só após teste físico; matriz pública de equipamentos; GTA assistida com conectores por UF; API aberta; preço público sem taxa de setup; governança LGPD visível. Qualquer afirmação de ser "primeiro" ou "único" exige auditoria; ausência de menção a um recurso não prova ausência.

## Decisões pendentes (tratar como perguntas, não como fato)
- Escopo bubalino. App único vs apps por perfil (hipótese: fluxos separados por papel na mesma plataforma).
- Mínimo de identificação individual vs inventário por lote, com migração gradual sem fingir identidade individual.
- Financeiro básico no plano de entrada? API pública em todos os planos ou addon?
- Validar legislação e preços de mercado em fonte primária vigente antes de prometer diferencial, preço ou cronograma.

## Métricas de produto (baseline no discovery/piloto, depois metas)
Sucesso de sincronização (capturado/enviado/aceito/duplicado/rejeitado), completude de cadastro, tempo para registrar manejo, pesagens por intervalo, adoção por papel, erro de identificação, confiança no indicador, retenção, custo de suporte/hardware. Metas comerciais antigas são hipóteses, não metas validadas.
