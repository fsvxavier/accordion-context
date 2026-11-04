# Pastagens, piquetes e lotação

## Cadastro
Área/piquete: hectares, capacidade configurável, geometria/localização, status (ocupado, descanso, reforma), instalações (bebedouro, cocho, saleiro, cerca). Mapa interativo com lançamento direto pelo mapa e troca de lote de piquete pelo mapa é capacidade esperada. Imagem de satélite, NDVI e clima: P2, só após validar origem/licença, resolução, cobertura e tratamento explícito de estimativa.

## Movimentação e ocupação
Lote entra/sai com data; histórico append-only; dias de ocupação e de descanso por piquete; alerta de dados incompletos (lote sem saída, área sem hectares). Percentual de ocupação e taxa de ocupação por propriedade e por lote.

## Lotação
```
UA = peso vivo (kg) / 450
Lotação (UA/ha) = Σ UA dos animais no piquete / área útil (ha); decimal exato, sem float, e indicador, não prescrição
```
Definição de unidade animal, peso de referência, área e período são **configuráveis e expostos no relatório** (a referência 450 kg varia por fonte). Exibir se usa peso real da última pesagem ou peso de referência por categoria.

Lotação é indicador, não prescrição: o relatório diz isso e não classifica a carga como adequada ou excessiva sem parâmetro técnico configurado.

## Pastejo rotacionado e coletas
Conferência de pasto operacional (peão a pé): bebedouros, saleiros, cercas. Coletas de **escore personalizadas** definidas pelo usuário: escore de pasto, altura do pasto/resíduo, escore corporal (escala 1–5 sugerida), escore de cocho, de umbigo, voltagem de cerca. Estação águas/seca influencia capacidade de suporte e deve ser parâmetro.

## Indicadores
Lotação global (UA/ha), @/ha, kg/ha, GMD por piquete, dias de descanso, taxa de acúmulo (quando houver medição), custo por ha/ano (= valor_total_atividades / área). Monitoramento inteligente de pasto é recurso avançado (P2).

## Armadilhas
- Lotação sem exibir unidade e peso usados.
- Estimativa de satélite apresentada como medição.
- Mover lote sem deixar histórico de origem/destino.
