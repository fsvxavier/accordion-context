# Sanidade, vacinação e carência

## Eventos sanitários
Vacina, vermifugação, tratamento, exame, castração (só machos — validar por sexo), descorna, marcação. Campos: produto, **lote de fabricante e validade**, dose/unidade, via, data, animais ou lote, aplicador, veterinário responsável quando aplicável. Aplicação por lote gera vínculo por animal (necessário para carência e rastreabilidade individual).

## Protocolos e calendário
Protocolos e calendários configuráveis por propriedade e categoria (ex.: brucelose em fêmeas jovens, aftosa conforme programa vigente da UF, clostridioses, vermifugação estratégica). Defaults vêm identificados como **editáveis e não aconselhamento médico**, com referência validada por profissional. Alertas de evento previsto/atrasado; conclusão em lote offline com resumo e confirmação (P1). Relatório de "mapa sanitário" por lote/período.

## Carência (regra regulatória crítica)
Resíduo de medicamento causa condenação em frigorífico e barreira sanitária em exportação.
- **Modelo:** produto → regra de carência (finalidade abate/leite, dias, fonte [bula/registro MAPA], versão, vigência). Na aplicação, calcular e **persistir** `liberacao = data_aplicacao + dias` por animal; mudança futura da regra não reescreve o histórico. Várias aplicações ativas: vale a maior data de liberação.
- **Bloqueio:** seleção de animal sob carência ativa para venda/abate **sinaliza e bloqueia** com data de liberação e produto causador; permitir seguir com os animais livres do mesmo lote. Override só com perfil autorizado, justificativa obrigatória e auditoria. Histórico nunca apagado.
- Prazos nunca fixos no código: cadastro de produto/carência curado por responsável técnico, com fonte.
- Sinalização também no ato de planejar venda, na tela do animal e no relatório de abate.

## Estoque e baixa
Descontar estoque **somente após evento confirmado**, uma vez (idempotência offline). Correção = estorno rastreável. Controle de validade e lote do produto; alerta de mínimo/validade. Baixa por lote de produto no estoque Farmácia.

## Rastreabilidade sanitária
Eventos sanitários podem alimentar requisitos oficiais **somente conforme regra vigente validada por UF/atividade**. Vacinação contra brucelose é evento que, a partir de 2027, dispara obrigatoriedade de identificação individual (ver rastreabilidade): modelar esse evento como gatilho configurável, parametrizado por UF e vigência.

## Indicadores
Cobertura vacinal, atraso de manejo, mortalidade por causa, custo sanitário por cabeça, dias de carência restantes, uso de produto por lote.

## Armadilhas
- Calcular carência na hora da consulta em vez de gravar com a regra da época.
- Alerta apenas informativo na venda.
- Baixa de estoque na criação do evento pendente (antes de confirmar).
- Defaults de protocolo apresentados como prescrição.
