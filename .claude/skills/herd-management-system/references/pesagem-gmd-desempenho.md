# Pesagem, GMD e desempenho

## Registro de pesagem
Individual e por lote. Campos: peso, **unidade** (kg), data efetiva (negócio) e data de recepção (sync), método (balança fixa/bastão/estimativa/fita), operador, origem manual ou equipamento, jejum quando aplicável. Pesagem rápida (só ID+peso) e completa (com outros manejos na mesma lida) são fluxos distintos. Pesagem na compra/venda é evento próprio ligado à transação.

## GMD (ganho médio diário)
```
GMD (kg/dia) = (peso_atual − peso_anterior) / dias_entre_pesagens
```
- Entre pesagens **válidas do mesmo animal**; mostrar intervalo, pesagens-base e fórmula (memória de cálculo).
- Se datas ou pesos inválidos (intervalo ≤ 0, peso ≤ 0, fora de faixa), **não calcular** e explicar o motivo.
- Exibição típica: 3 casas decimais (ex.: 0,891 kg/dia); decimal exato internamente, nunca float. **Arredondamento padrão: meio para cima (half-up) só na exibição**; o cálculo usa o valor cheio, e tela, relatório e exportação aplicam a mesma regra. Mudar o modo é decisão de projeto versionada.
- GMD de lote/rebanho considera apenas animais com GMD calculável (a primeira pesagem não tem intervalo). **Padrão: média ponderada por dias** = Σ ganho de peso / Σ dias dos animais elegíveis; a média simples dos GMDs individuais pode ser exibida à parte, sempre rotulada. Mostrar quantos animais entraram e quantos foram excluídos, com motivo.
- GMD fora da faixa plausível (negativo, ou muito acima do esperado para a categoria) gera sinalização para revisão, não bloqueio nem correção automática.
- Duas pesagens no mesmo dia: manter ambas como eventos; a vigente é a versão escolhida/última válida; GMD usa a vigente. Intervalo zero ⇒ sem GMD.
- **Correção** = nova versão ligada à original (antes/depois, motivo, ator). Relatório mostra valor vigente e trilha. Jamais apagar o original.
- Pesagem de saída sem jejum padronizado distorce GMD; guardar condição.
- GMD anual do rebanho deriva de rebanho médio e arrobas produzidas (declarar método).

## Curvas e ficha
Gráfico de peso com GMD sobreposto, evolução com fotos, comparação com melhores desempenhos do lote, previsão de abate a partir de GMD e peso atual (cenário, premissas editáveis, não garantia).

## Peso ajustado e idades-alvo
Peso ajustado a idade-alvo (ex.: 205 dias à desmama; 365 e 550 dias na recria) só após validar fórmula, idade e tratamento do peso ao nascer com responsável técnico. Forma geral de referência (validar com zootecnista):
```
P205 = ((peso_desmama − peso_nascer) / idade_dias) × 205 + peso_nascer
```
Sem peso ao nascer, declarar a aproximação usada.

## Agendamento
Pesagem por lote/objetivo configurável; padrão sugerido ≠ prescrição zootécnica.

## Indicadores de produção de carne
- **Arrobas produzidas** (@prod) = (estoque_final − estoque_inicial) + compras − vendas, em @ (1 @ = 15 kg de carcaça em bovinos). Mede produção interna.
- **Taxa de desfrute** (%) = (EF − EI − Compras + Vendas) / EI × 100, em cabeças ou kg (período padrão 1 ano). Mede excedente comercializável. **Sinais de compras/vendas são opostos** e ambos corretos; os dois coexistem com definição na tela.
- **Produção real da vaca** = (peso_bezerro × 365) / intervalo_entre_partos.
- **Relação de desmama** = peso_bezerro × 100 / peso_fêmea.
- **@/ha/ano**, **custo por @ produzida** = custo_total_produção / @prod.
- **Rendimento de carcaça** (%) = peso carcaça quente / peso vivo × 100; ver abate.

## Testes obrigatórios
Table-driven e property-based: intervalos limite, datas iguais, peso zero/negativo, correção posterior, arredondamento, equivalência entre tela, relatório e exportação. Erro em GMD é falha existencial em produto que vende precisão.
