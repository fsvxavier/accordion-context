# Confinamento

Fase P1/F2; liberar só após validar processo e fórmula com fazendas piloto. Confinamento como operação gerenciada (baias, tratos, cocho, MS) é mais que tratar o confinamento como centro de custo.

## Estrutura
Unidade de confinamento → baias/currais, linhas e ruas (visual). Lotes de entrada/saída com capacidade, pesagem de entrada, ciclo (dias de cocho) e pesagem de saída. Abertura e fechamento de lote.

## Dieta e trato
- Dieta por lote: ingredientes, quantidade em matéria natural e **matéria seca (MS)**, unidade, custo vigente. Tabela de ingredientes com MS%.
- **Trato planejado × realizado**: lançamento manual ou automatizado; **batidas** (misturador/vagão), horário, executor, divergência. Painel de balança no vagão forrageiro é hardware relevante.
- **Leitura de cocho** (escore) por baia no app, com foto/vídeo/áudio de ocorrência.
- Ajuste de trato a partir da leitura; projeção de consumo e eficiência dos tratos.

## Indicadores
Consumo total/médio por lote, consumo de MS (kg MS/animal/dia, % do peso vivo), GMD do ciclo, **conversão alimentar** = kg MS consumida / kg ganho, custo diário por cabeça (= custo diário do lote / nº de animais), custo da @ produzida no confinamento, rendimento de carcaça esperado. Exibir janela, peso, MS e método; **não comparar ciclos com bases inconsistentes**.

## Comparar ciclos e lotes (regra de comparação)
- Comparável só com **mesma base declarada**: janela (dias de cocho), peso de entrada e saída, MS%, categoria e critério de custo. Ciclos de duração diferente (ex.: 90 × 120 dias) são exibidos lado a lado com a janela de cada um, ou normalizados para janela/peso-alvo comum **declarados**; nunca ranqueados como se a base fosse igual.
- Conversão sempre em **kg MS consumida / kg ganho** (nunca matéria natural). Peso de saída sem jejum padronizado entra com flag.
- Custo diário por cabeça: consumo **realizado** × preço vigente do ingrediente na data (ou custo médio do estoque, declarado), mais itens de custo listados (mão de obra, depreciação, sanidade) com indicação de incluído/excluído. Aquisição do animal fica fora do desembolso diário.
- Morte, saída ou troca de lote durante o ciclo: animal sai do denominador a partir da data, com seus dias de cocho contados; o painel mostra quantos entraram, saíram e foram excluídos, com motivo.
- CA do lote = Σ MS consumida / Σ ganho dos animais elegíveis (ponderada por construção, não média de CAs individuais). Fase de adaptação da dieta é janela separada, declarada e opcionalmente excluída do comparativo. MS% do ingrediente é registrada por lote de ingrediente/data de uso; consumo em MS usa a MS% vigente na data do trato.
- Normalizar duas janelas: preferir comparar na mesma janela de dias de cocho (corte do mais longo) ou no mesmo peso de saída, sempre declarando a base; se não for possível, exibir sem ranking.
- Faixas de referência de CA/GMD por dieta não vêm prontas: são parâmetros configuráveis a validar com o nutricionista.

## Cenário de abate
Projeção de custo e abate é *cenário* com premissas editáveis, nunca garantia (P2). Simulador de venda por kg e por @.

## Integrações
Estoque de ração baixa por batida; custo flui para financeiro por lote; peso de saída alimenta abate/frigorífico.

## Armadilhas
- Misturar MS e matéria natural no mesmo indicador.
- Comparar conversão de ciclos com janelas distintas.
- Trato realizado sobrescrevendo planejado (precisa manter os dois).
