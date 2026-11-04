# Estoque e compras

## Três estoques especializados 
1. **Farmácia** (medicamentos, vacinas) — lote de fabricante, validade, baixa no evento sanitário confirmado.
2. **Nutricional** (sal mineral, proteinado, ração, silagem, ingredientes) — valor unitário com alta precisão decimal; baixa por trato/lançamento nutricional, conferência consumo real × planejado.
3. **Biológico** (sêmen, embriões) — botijão/caneca/dose; ver reprodução.
Estoque pode ser compartilhado entre fazendas da mesma conta ou isolado por propriedade (configuração).

## Ledger
- **Saldo derivado de ledger** de eventos: recebimento, consumo, perda, transferência, ajuste. Nunca edição livre do saldo.
- Cadastro de insumo: categoria, unidade, conversão de unidade, lote/validade quando aplicável, estoque mínimo.
- Consumo vinculado a manejo/animal/lote; alerta por mínimo e validade; origem do saldo visível.
- Múltiplos almoxarifados, contagem física e conciliação de diferença com aprovação (P1).
- **Custo médio com recálculo retroativo** ao remover/editar entrada ou saída: fonte clássica de bug financeiro — exige teste de propriedade.
- **Baixa automática na sincronização do campo** (app do peão deduz sal, proteinado, medicamentos usados no dia). Idempotente: retry offline não baixa duas vezes.
- Compras: entrada manual, importação de NF-e (XML/chave) em plano superior; conta a pagar gerada.

## Checklist de baixa e alertas
Baixa só após o evento confirmado; correção por estorno (movimento append-only ligado ao original); reenvio offline não baixa duas vezes. Todo item tem lote e validade, com alerta de estoque mínimo e de vencimento.

## Armadilhas
- Baixa duplicada por reenvio.
- Saldo negativo silencioso por evento fora de ordem (definir política: bloquear ou sinalizar).
- Estoque biológico fora do mesmo ledger e sem validade.
