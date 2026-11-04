# Indicadores, relatórios, alertas e API de saída

## Regra de ouro
Todo indicador declara: fórmula, população elegível, período/janela, unidade, tratamento de ausência/outlier, arredondamento, fonte, **horário da última atualização** e exclusões. Nunca oculta dados ausentes ou premissas. Definições coexistentes (ex.: @ produzida × desfrute) mostram ambas com nome claro. Implementação única com paridade tela/relatório/exportação.

## Catálogo de indicadores
| Área | Indicadores |
|---|---|
| Produtivo | GMD (animal, lote, rebanho), GMD anual, peso ajustado, @ produzidas, @/ha, lotação UA/ha, rebanho médio, produção real da vaca, relação de desmama |
| Reprodutivo | prenhez, natalidade, concepção, doses/prenhez, aproveitamento de fêmeas, taxa de desmame, kg desmamado/vaca exposta, IEP, idade ao 1º parto, desfrute |
| Sanitário | cobertura vacinal, mortalidade por causa, carência ativa, custo sanitário |
| Confinamento | consumo MS, conversão alimentar, custo diário, @ do ciclo |
| Econômico | custo/cabeça/dia, desembolso/cabeça/mês, custo por @, margem/ha, DRE, fluxo de caixa, preço médio da @, rentabilidade |
| Abate | rendimento de carcaça, @ vendida, condenação |
Ordem de grandeza esperada para paridade: dezenas de indicadores (≈50) e de relatórios (30+); comparação resultado × meta (objeto "Meta").

## Relatórios MVP
Inventário, movimentos, pesagens/GMD, reprodução, sanidade; filtráveis por fazenda/período/lote; exportação com data, origem e permissão (CSV/PDF/impressão mobile). Outros: individual e geral de produção animal, mortalidade, previsão de parto, desempenho reprodutivo (IEP, último parto), estações, chuvas (pluviometria: dias de chuva, média por dia), fluxo de caixa, recebimentos/pagamentos, movimentações. Relatórios personalizados e gráficos em plano superior.

## Alertas e notificações (P1)
Por usuário e canal (push, e-mail, WhatsApp/SMS); preferências próprias; **alertas operacionais separados de marketing** e desligáveis só este. Eventos: IATF e vacina/vermifugação a vencer, carência, estoque mínimo/validade, contas a vencer, certificado digital a vencer, tarefa atrasada, sincronização pendente, manutenção. Relatórios enviados por WhatsApp agendados.

## API / webhooks (P2)
Versionados, escopos de acesso, limites, idempotência, revogação, logs de consumo. Modelo comercial pendente (API aberta em todos os planos é diferencial candidato; restringi-la a planos altos reduz adoção). Outbox alimenta webhooks.
