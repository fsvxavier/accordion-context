# Fórmulas de pecuária

> Detalhe e contexto de cada tema nos arquivos temáticos: [01 sistemas de produção e intensificação](01-sistemas-producao-intensificacao.md) · [02 reprodução e genética](02-reproducao-genetica.md) · [03 cria, bezerros e recria](03-cria-bezerros-recria.md) · [04 nutrição e suplementação](04-nutricao-suplementacao.md) · [05 confinamento e terminação](05-confinamento-terminacao.md) · [06 pastagem, adubação e forragem](06-pastagem-adubacao-forragem.md) · [07 sistemas integrados e silvipastoril](07-sistemas-integrados-silvipastoril.md) · [08 leite, qualidade e ordenha](08-leite-qualidade-ordenha.md) · [09 conforto, bem-estar, instalações e pré-abate](09-conforto-bem-estar-instalacoes-pre-abate.md) · [10 sanidade](10-sanidade.md) · [11 gestão, economia e finanças](11-gestao-economia-financas.md) · [12 legislação ambiental e rastreabilidade](12-legislacao-ambiental-rastreabilidade.md) · [13 sustentabilidade, mercado e certificação](13-sustentabilidade-mercado-certificacao-territorio.md) · [14 gestão do conhecimento e inovação](14-gestao-conhecimento-inovacao.md). Índice: [README](README.md).

## Como ler

- Cada área traz um bloco de equações, uma por linha, com ID (por exemplo DE1). A tabela logo abaixo diz o nome, quando usar e os dados necessários de cada equação.
- Todos os símbolos estão na Legenda de símbolos, com a unidade. Use sempre as quantidades nas unidades da legenda.
- Operadores: × ÷ − +, Σ (soma), √, ^ (potência), MAX, MIN, SE(condição; a; b) e arredondar_cima(x). Constantes padrão aparecem entre parênteses na legenda (por exemplo KC = 15, KVA = 30, RC = 0,505, DM = 30).
- Símbolos com colchetes ou subíndice, como Cmc[k] ou Cing_i, valem para cada item ou ingrediente.

## Legenda de símbolos

| Símbolo | Significado | Unidade |
|---|---|---|
| CA | Conversão alimentar | kg MS/@ ou kg MS/kg |
| CMSt | Consumo total de MS do lote | kg MS |
| dias | Duração do período (entre pesagens, ciclo, confinamento, permanência ou recria) | dias |
| dias_arr | Dias para ganhar 1 arroba | dias/@ |
| dias_águas | Dias do período das águas | dias |
| DM | Dias por mês (mês comercial, constante = 30) | dias/mês |
| GMD | Ganho médio diário | kg/dia |
| GMDáguas | Ganho médio diário da recria nas águas | kg/dia |
| Gseca | Ganho de peso da recria na seca | kg |
| Gáguas | Ganho de peso da recria nas águas | kg |
| KC | Quilos de carcaça por arroba (constante = 15) | kg/@ |
| KVA | Quilos de peso vivo (ou de ganho) por arroba (constante = 30) | kg/@ |
| Narr | Arrobas de carcaça por animal | @/animal |
| Narrv | Arrobas por peso vivo (em EC, com PV igual ao peso da compra) | @ |
| Nmeses | Prazo até o peso final (meses de custo recorrente) | meses |
| Pcarc | Peso de carcaça | kg |
| PCf | Peso de carcaça final | kg |
| PCi | Peso de carcaça inicial | kg |
| Pdesm | Peso à desmama | kg |
| Pent | Peso de entrada no confinamento | kg |
| Pf | Peso final | kg |
| Pi | Peso inicial | kg |
| Pmed | Peso médio do período | kg |
| PV | Peso vivo | kg |
| Qprod | Produção total de carcaça (arrobas ou kg) | @ ou kg |
| RC | Rendimento de carcaça (constante padrão = 0,505) | fração |
| RGP | Rendimento do ganho de peso em carcaça | % |
| ΔP | Peso a ganhar, ou ganho total de peso | kg |
| Amb | Efeito do ambiente | unidade da característica |
| Dand | Data do exame andrológico | data |
| Dchuvas | Data de início das chuvas | data |
| Ddiag | Data mínima do diagnóstico de gestação | data |
| Ddose1 | Data da primeira dose da vacina | data |
| DEPA | DEP do touro A | unidade da característica (kg) |
| DEPB | DEP do touro B | unidade da característica (kg) |
| Dmf | Data de fim da monta | data |
| Dmi | Data de início da monta | data |
| Dref | Data do reforço vacinal | data |
| Fcons | Coeficiente de consanguinidade | % |
| Fen | Fenótipo | unidade da característica |
| g | Número de gerações de cruzamento absorvente | gerações |
| G | Efeito do genótipo | unidade da característica |
| GxA | Interação entre genótipo e ambiente | unidade da característica |
| H | Heterose | % |
| Het | Heterozigose no cruzamento absorvente | % |
| Mpre | Mortalidade pré-desmama | % |
| Ndesc | Matrizes descartadas | cab |
| Nfrep | Fêmeas em reprodução | cab |
| Nmort | Bezerros mortos até a desmama | cab |
| Nnasc | Bezerros nascidos | cab |
| Padulto | Peso adulto do rebanho | kg |
| Pmonta | Peso da novilha para entrar na monta | kg |
| Rpar | Parentesco entre os pais | % |
| Trep | Taxa de reposição de matrizes | % |
| X1 | Média da cria F1 | unidade da característica |
| X2 | Média dos pais | unidade da característica |
| ΔDEP | Diferença entre os touros pela DEP | unidade da característica (kg) |
| aj | Ajuste da ração pelo escore de cocho (+0,10; +0,05; 0; −0,05; −0,10) | fração |
| Cbatida | Custo total da batida | R$ |
| Ccons | Custo diário de consumo (suplemento) por animal | R$/animal/dia |
| Ced | Economia por kg de MS na dieta | R$/kg MS |
| Cing_i | Custo do ingrediente i na batida | R$ |
| Ckg | Custo por kg do suplemento ou do ingrediente | R$/kg |
| Ckg_i | Custo por kg do ingrediente i | R$/kg |
| CkgMS | Custo da dieta por kg de MS | R$/kg MS |
| Cmist | Custo por kg da mistura | R$/kg |
| CMS | Consumo de matéria seca da dieta | kg MS/animal/dia |
| CMScalor | Consumo de MS sob calor | kg MS/animal/dia |
| Cmês | Custo mensal de suplemento por animal | R$/animal/mês |
| Cons | Consumo diário por animal (dieta ou suplemento) | kg/animal/dia |
| Cper | Custo de suplemento no período por animal | R$/animal |
| Csaco | Custo do saco da mistura | R$/saco |
| Csup | Custo de suplementação do lote no ciclo | R$ |
| Dener | Déficit energético do bezerro | Mcal ED/dia |
| Econ_dieta | Economia total com a redução do custo da dieta | R$ |
| Eing | Estoque do ingrediente | kg MN |
| Eleite | Energia fornecida pelo leite ao bezerro | Mcal ED/dia |
| fing | Fração do ingrediente na MS da dieta | fração |
| fPV | Consumo como fração do peso vivo | fração do PV |
| Ksaco | Peso do saco | kg/saco |
| Ktotal | Peso total da batida | kg |
| mi | Quantidade do ingrediente i na batida | kg |
| Mleite | Consumo de leite do bezerro | kg/dia |
| MNing | Matéria natural do ingrediente por animal | kg MN/animal/dia |
| MSing | Teor de MS do ingrediente | fração |
| Ncab | Número de animais (cabeças) do lote, do evento ou da dívida | cab |
| Nener | Necessidade total de energia do bezerro | Mcal ED/dia |
| Nsacos | Número de sacos por dia | sacos/dia |
| Pacum_i | Peso acumulado até o ingrediente i | kg |
| Pbatida | Peso da batida | kg |
| pcti | Percentagem do ingrediente i na batida | % |
| Psaco | Preço do saco | R$/saco |
| Qlote | Quantidade diária do lote | kg/dia |
| Ração_ant | Ração do dia anterior | kg |
| Ração_novo | Ração ajustada do dia | kg |
| rcalor | Redução do consumo sob calor | fração |
| red | Redução percentual do custo da dieta | fração |
| s | Sentido do ajuste do trato (+1 aumenta, 0 mantém, −1 reduz) | adimensional |
| Spct | Soma das percentagens da receita | % |
| Trato | Trato anterior | kg MN |
| Trato_novo | Trato ajustado | kg MN |
| Apast | Área de pastagem efetivamente pastejada (em PA9, a área necessária para o lote) | ha |
| Arg | Teor de argila do solo | % |
| Atot | Área total dos piquetes | m² ou ha |
| Cabm | Média mensal de cabeças da categoria | cab |
| Ccalc | Custo do calcário | R$ |
| efur | Eficiência de aproveitamento do N da ureia (0,50) | fração |
| Estac | Estacionalidade da produção de forragem | % |
| Gcab | Ganho de peso por cabeça no ano | kg/cab/ano |
| Gcal | Ganho em R$ com a MS adicional da calagem | R$ |
| GPV | Ganho de peso vivo do rebanho no ano | kg |
| icv | Índice de conversão da categoria em UA | UA/cab |
| kP | Dose de P2O5 por 1% de argila (3 a 5) | kg P2O5/ha por % |
| Ksem | Quantidade de sementes corrigida pelo valor cultural | kg/ha |
| Lalvo | Lotação alvo | cab/ha |
| Lotalta | Lotação das fazendas de alto desempenho | cab/ha |
| Lotcab | Taxa de lotação em cabeças | cab/ha |
| Lotmed | Lotação média geral (1,21) | cab/ha |
| LotUA | Taxa de lotação em UA | UA/ha |
| MS | Produção de MS da forragem | kg MS/ha.ano |
| MScom | Produção de MS com calagem | t MS/ha.ano |
| MSseca | MS produzida na seca | kg MS/ha |
| MSsem | Produção de MS sem calagem | t MS/ha.ano |
| MStot | MS total anual | kg MS/ha |
| Narrha | Arrobas produzidas por hectare | @/ha.ano |
| Ndose | Dose de nitrogênio | kg N/ha.ano |
| NMO | N fornecido pela matéria orgânica do solo | kg N/ha.ano |
| Npiq | Número de piquetes | un. |
| P2O5 | P2O5 por teor de argila | kg P2O5/ha |
| Pcarcha | Produtividade em carcaça | kg carcaça/ha |
| PD | Período de descanso do piquete | dias |
| PO | Período de ocupação do piquete | dias |
| PrPVha | Produtividade pecuária em peso vivo | kg PV/ha.ano |
| Ptotal | Peso total do lote | kg |
| PUA | Peso de 1 UA (constante padrão = 450) | kg PV/UA |
| Rcal | Retorno econômico da calagem | fração |
| Rlot | Razão entre a lotação de alto desempenho e a média geral (3,12) | adimensional |
| SPVha | Semente pura viável recomendada | kg/ha |
| SSP | Superfosfato simples | kg/ha |
| Tpiq | Área de cada piquete | m² ou ha |
| UA | Unidades animais do lote | UA |
| UAcat | UA da categoria | UA |
| Ureia | Ureia necessária | kg ureia/ha |
| VC | Valor cultural da semente | % |
| ΔMScal | Ganho de MS pela calagem após desconto | t MS/ha.ano |
| ΔPsolo | Aumento desejado de P no solo | mg/dm³ |
| Aprod | Área produtiva do módulo | ha |
| Cbez | Valor do leite consumido por bezerro | R$/bezerro |
| Cord | Custo da ordenha manual | R$ |
| dalim | Dias de aleitamento | dias |
| IP | Intervalo entre partos | meses |
| Lalq | Produtividade por alqueire | L/alqueire.ano |
| Lano | Produção anual do rebanho | L/ano |
| Ldb | Leite por dia por bezerro | L/dia |
| Lha | Produtividade de leite por hectare | L/ha.ano |
| Litros | Litros ordenhados | L |
| Lmd | Produção média diária de um mês | L/dia |
| Lvd | Litros por vaca por dia | L/vaca.dia |
| mam | Massa da amostra de leite | kg |
| N | Contagem bacteriana no instante t | UFC/mL |
| N0 | Contagem bacteriana inicial | UFC/mL |
| Nalq | Número de alqueires | alqueires |
| Nvac | Número de vacas em lactação | vacas |
| Pant | Produção por vaca por ordenha antes da climatização | kg/vaca/ordenha |
| Pdep | Produção por vaca por ordenha depois da climatização | kg/vaca/ordenha |
| PerdaCCS | Perda de produção por CCS alta | L/dia |
| PL | Período de lactação | meses |
| Plact | Produção da lactação | L |
| Pleite | Preço do leite | R$/L |
| Pord | Custo da ordenha manual por litro (constante = 0,17) | R$/L |
| Pvl | Percentual de vacas em lactação | % |
| Qmod | Produção diária do módulo | L/dia |
| Rleite | Renda bruta de leite por hectare | R$/ha.ano |
| tref | Tempo fora da refrigeração | min |
| Vam | Volume da amostra de leite | L |
| Vtanque | Capacidade do tanque de expansão | L |
| Δclim | Ganho de produção por climatização | % |
| ρ | Densidade do leite | kg/L |
| Asombra | Área mínima de sombra | m² |
| asombra | Sombra por animal | m²/animal |
| Calvo | Teor de cloro desejado | mg/L |
| Csan_antes | Custo sanitário por cabeça antes da mudança | R$/cab |
| Csan_dep | Custo sanitário por cabeça depois da mudança | R$/cab |
| D1000 | Dose do produto para 1 mg/L em 1.000 L | mL ou g |
| Dcl | Dose do produto clorado para o reservatório | mL ou g |
| Ddose | Dose total do medicamento para o animal | unidade da dose (UI ou mg) |
| ddose | Dose por kg de peso vivo | unidade da dose por kg |
| dprod | Volume de produto por litro de água (água sanitária 10; hipoclorito 2) | mL/L |
| Econ_san | Economia sanitária por cabeça | R$/cab |
| hdesc | Horas de descanso após a viagem | h |
| hviag | Horas de viagem | h |
| Larvas | Larvas evitadas | larvas/dia |
| mdet | Massa de pó de detergente | g |
| Naloj | Animais (vacas) alojados | cab |
| Nbaias | Número de baias | baias |
| Ncasco | Vacas a avaliar no escore de casco | vacas |
| Nfem | Fêmeas ingurgitadas retiradas por dia | fêmeas/dia |
| Nhem | Hematomas por carcaça | n/carcaça |
| Nhig | Vacas a avaliar no escore de higiene | vacas |
| Nobs | Vacas observadas | vacas |
| Nsinais | Vacas com sinais de estresse térmico | vacas |
| Pdesc | Perda de peso no descanso | kg |
| PerdaVerm | Perda máxima de leite por verminose | L ou kg |
| Pjej | Perda de peso no jejum | kg |
| pjej | Perda de peso no jejum como fração do PV (0,0276 a 0,0418) | fração |
| Prev | Prevalência de estresse térmico | % |
| pverm | Fração máxima do leite perdida por verminose (0,20) | fração |
| Qleite | Produção de leite do rebanho | L ou kg |
| Qágua | Consumo de água do lote | L/dia |
| qágua | Consumo de água por animal (50 a 60) | L/animal/dia |
| Rbaia | Razão de animais por baia | adimensional |
| Vprod | Volume de produto para a solução | mL |
| Vres | Volume do reservatório | L |
| Vsol | Volume da solução de detergente | L |
| Vág | Volume de água da solução | L |
| aanim | Área por animal no piquete | m²/animal |
| Acreep | Área do creep-feeding | m² |
| Aembute | Área do embute | m² |
| Akm | Arrobas de mão de obra por km de cerca (12) | @/km |
| Apiq | Área do piquete | m² |
| Arem | Área da remanga | m² |
| Aser | Área da seringa | m² |
| Carame | Custo do arame da cerca | R$ |
| Ccerca | Custo total da cerca | R$ |
| Cest | Custo dos esticadores | R$ |
| Ckm | Custo da cerca por km | R$/km |
| Cmocerca | Custo de mão de obra da cerca | R$ |
| Cpiq | Comprimento do piquete | m |
| Cport | Comprimento da porteira | m |
| Cpostes | Custo dos postes | R$ |
| Cveic | Comprimento do maior veículo | m |
| ecocho | Espaço de cocho por animal | m/animal |
| Esp | Distância entre postes | m |
| Etot | Esticadores totais | un. |
| Fios | Número de fios da cerca | un. |
| km | Quilômetros de cerca a executar ou distância do frete | km |
| Lbeb | Comprimento linear de bebedouro | m |
| lbeb | Espaço linear de bebedouro por animal (0,10) | m/animal |
| Lcocho | Comprimento de cocho do lote | m |
| Lcreep | Comprimento de cocho do creep | m |
| lcreep | Espaço de cocho do creep por bezerro (0,10) | m/bezerro |
| mcab | Cocho linear por cabeça | m/cab |
| Mcerca | Metros totais de cerca | m |
| Mkm | Metros de cerca por km base (1.000) | m/km |
| MOkm | Mão de obra por km de cerca | R$/km |
| Napiq | Animais por piquete | cab |
| Nbeb | Animais bebendo ao mesmo tempo | cab |
| Nbez | Número de bezerros | cab |
| Nbsim | Bebedouros simultâneos no alojamento | un. |
| Ncub | Número de cubículos | un. |
| Nest | Esticadores por km | un./km |
| Nmo | Quantidade de mão de obra por km | un./km |
| Npiqc | Número de piquetes de confinamento | un. |
| Pest | Preço do esticador | R$/un. |
| Postes | Postes por km | un./km |
| Pposte | Preço do poste | R$/un. |
| Prolo | Preço do rolo de arame | R$/rolo |
| Ptot | Postes totais | un. |
| Rolkm | Rolos de arame por km | rolos/km |
| Rolot | Rolos de arame totais | rolos |
| Rpatio | Raio do pátio do embarcadouro | m |
| Varr | Valor da arroba | R$/@ |
| Wpiq | Largura do piquete | m |
| aF | Alíquota de FGTS (0,08) | fração |
| Agd | Ágio a diluir por dia | R$/dia |
| Agt | Ágio total a diluir por animal | R$ |
| aI | Alíquota de INSS de outras entidades (0,027) | fração |
| Arrm | Arrendamento mensal por cabeça | R$/cab/mês |
| Cab | Cabeças do rebanho no período | cab |
| Cant | Custo total do animal | R$/animal |
| Caq | Valor de aquisição do animal | R$/animal |
| Caqu | Custo de aquisição de instalação ou máquina | R$ |
| Car | Custo do suplemento por arroba produzida | R$/@ |
| Carc | Custo da arroba na compra | R$/@ |
| Carop | Custo operacional da arroba | R$/@ |
| Carr | Custo de arrendamento do ciclo | R$ |
| Cart | Custo da arroba pelo custo total | R$/@ |
| CF | Cabeças por funcionário no rateio de mão de obra (1.000) | cab/func |
| Cfunc | Custo mensal por funcionário | R$/mês |
| Cgrp | Custo mensal do grupo de funcionários | R$/mês |
| CIATF | Custo de IATF do lote | R$ |
| Citem | Custo de um item (por item k) | R$ |
| Ckgc | Custo do kg vivo na compra | R$/kg |
| Cman | Custo anual de manutenção | R$/ano |
| Cmc | Custo por cabeça por mês de cada item (por item k) | R$/cab/mês |
| Cmcab | Custo mensal total por cabeça (sem reposição) | R$/cab/mês |
| Cmo | Custo de mão de obra do ciclo | R$ |
| Cout | Custo de outras despesas do ciclo | R$ |
| Cprod | Custo de produção do período por cabeça | R$/cab |
| Crep | Custo de reposição | R$ |
| Ctot | Custo total do ciclo | R$ |
| Cvet | Custo de veterinária do ciclo | R$ |
| CY | Custo de produzir só o produto i (por produto i) | R$ |
| CYc | Custo conjunto de produzir todos os produtos | R$ |
| D13 | Provisão de décimo terceiro | R$/mês |
| Dag | Ágio por arroba | R$/@ |
| Dd | Diária de um item (por item k) | R$/cab/dia |
| Ddiar | Despesa diária acumulada | R$ |
| Dext | Despesa extra | R$ |
| Diaria | Diária total (custo por cabeça por dia) | R$/cab/dia |
| DIATF | Despesa de IATF por cabeça | R$/cab |
| DifAr | Diferença entre preço e custo operacional da arroba | R$/@ |
| Dir | Direitos trabalhistas mensais (férias + 1/3 + 13º) | R$/mês |
| Dmens | Despesa mensal de um item (por item k) | R$/mês |
| Dop | Despesas operacionais do mês (sem reposição, comissão e frete) | R$/mês |
| EE | Economia de escopo | adimensional |
| Enc | Total de encargos | R$/mês |
| fag | Fator de ágio (1,15) | adimensional |
| farr | Fração da @ paga como arrendamento mensal (0,18) | fração |
| Fat | Fator de custo do funcionário | adimensional |
| Fer | Provisão de férias | R$/mês |
| FGTS | FGTS mensal sobre o salário | R$/mês |
| FGTSd | FGTS sobre direitos | R$/mês |
| Gx | Ganho extra necessário para diluir o ágio | kg/dia |
| Gxg | Ganho extra necessário em gramas | g/dia |
| INSSd | INSS sobre direitos | R$/mês |
| INSSo | INSS de outras entidades sobre o salário | R$/mês |
| mF | Multa do FGTS (0,4) | fração |
| MultD | Multa do FGTS sobre direitos | R$/mês |
| MultM | Multa do FGTS sobre a remuneração | R$/mês |
| NF | Número de funcionários | func |
| Outm | Outras despesas por cabeça por mês | R$/cab/mês |
| p13 | Percentual de décimo terceiro (0,0833) | fração |
| Pcomp | Peso do animal na compra | kg |
| PE | Ponto de equilíbrio da arroba | R$/@ |
| pEnc | Encargos sobre o salário | fração |
| pFer | Percentual de férias (0,0833) | fração |
| Pkg | Valor do kg vivo | R$/kg |
| Pkgag | Valor do kg vivo com ágio | R$/kg |
| pman | Percentual anual de manutenção (0,08 ou 0,065) | fração/ano |
| pProv | Provisões sobre o salário | fração |
| Prov | Total de provisões trabalhistas | R$/mês |
| pTer | Percentual de 1/3 de férias (0,0278) | fração |
| Pu | Preço unitário de referência do bezerro | R$/cab |
| Qarr | Arrobas produzidas | @ |
| RT | Relação de troca | bezerros/animal |
| Sal | Salário mensal do funcionário | R$/mês |
| Ter | Provisão de 1/3 de férias | R$/mês |
| Vaq | Valor de aquisição por cabeça | R$/cab |
| Vbez | Valor do bezerro | R$/cab |
| Vcomp | Valor pago na compra do animal | R$/animal |
| Vetm | Veterinária por animal por mês | R$/cab/mês |
| Vvenda | Valor de venda do animal gordo | R$/animal |
| Acg | Resultado geral acumulado trazido de anos anteriores | R$ |
| Acum | Resultado financeiro acumulado do ano anterior | R$ |
| Ajus | Ajuste de caixa | R$ |
| Amod | Área do módulo | ha |
| Apagar | Contas a pagar de gado | R$ |
| Aporte | Aporte de capital sugerido | R$ |
| Cabex | Cabeças adicionais compráveis com o resultado | cab |
| Cadq | Custo de aquisição do rebanho | R$ |
| Cadqcab | Custo de aquisição por cabeça | R$/cab |
| Cadqd | Cabeças adquiridas no período | cab |
| Caixa | Caixas e bancos | R$ |
| Capital | Capital inicial | R$ |
| Ccab | Custo de aquisição por cabeça no ano | R$/cab |
| Cdem | Demais custos do consolidado | R$ |
| Cger | Custo geral do consolidado | R$ |
| Cmp | Custos de compra (reposição + comissão + frete) | R$ |
| Com | Comissão de compra | R$ |
| Compra | Valor total das compras | R$ |
| Copor | Custo de oportunidade da terra e do capital | R$ |
| COT | Custo operacional total | R$/ha.ano |
| Ct | Custo no ano t | R$ |
| Cvend | Cabeças vendidas | cab |
| Dcab | Despesa por cabeça no ano | R$/cab/ano |
| Dcabm | Despesa por cabeça por mês | R$/cab/mês |
| Dcat | Despesa de uma categoria (por categoria k) | R$ |
| Dcic | Despesas de manutenção do ciclo | R$ |
| Ditem | Despesa de um item no ano (por item) | R$ |
| Divida | Total de vencimentos de dívidas | R$ |
| Dtot | Total de despesas do mês ou do ano | R$ |
| Dtotm | Total de despesas do mês | R$ |
| Est | Valor do estoque de rebanho | R$ |
| Frete | Valor do frete | R$ |
| i | Taxa de desconto (padrão 0,10) | a.a. |
| Iant | Investimentos dos anos anteriores | R$ |
| Icat | Investimento de uma categoria (por categoria k) | R$ |
| Itm | Valor de um item na mesma base da receita | R$ |
| Itot | Total de investimentos | R$ |
| Lcab | Resultado por cabeça no ano | R$/cab |
| Lecon | Lucro econômico | R$ |
| Lop | Lucro operacional | R$ |
| Margem | Margem percentual do resultado | % |
| Marr | Margem por arroba | US$/@ |
| Mdesp | Média de despesas por cabeça adquirida | R$/cab |
| Mha | Margem por hectare | US$/ha |
| Mit | Custo médio do item por cabeça adquirida (por item k) | R$/cab |
| Mper | Meses do período de cálculo (12) | meses |
| Pagos | Empréstimos pagos | R$ |
| pAP | Fração da receita bruta no aporte (0,8) | fração |
| Parrha | Produtividade em arrobas por hectare | @/ha |
| Pcab | Preço por cabeça | R$/cab |
| Pcr | Percentual sobre a receita | fração |
| Pvm | Preço médio de venda por cabeça | R$/cab |
| Qfr | Quantidade de fretes | un. |
| Rbruta | Receita bruta | R$/ha.ano |
| Rcat | Receita de uma categoria (por categoria k) | R$ |
| Rec | Receita de venda | R$ |
| Reccab | Receita por cabeça | R$/cab |
| Receb | Empréstimos recebidos | R$ |
| Repos | Valor de reposição do rebanho | R$ |
| Resano | Resultado financeiro do ano | R$ |
| Rescab | Resultado por animal | R$/cab |
| Rescab2 | Resultado por cabeça adquirida | R$/cab |
| Resemp | Saldo de empréstimos | R$ |
| Resmes | Resultado financeiro do mês | R$ |
| Resultado | Resultado do lote | R$ |
| Ret | Retiradas de capital | R$ |
| Rgeral | Resultado geral da atividade | R$ |
| Rkm | Frete por km do veículo | R$/km |
| RLha | Receita líquida por hectare | R$/ha.ano |
| Rmod | Receita líquida do módulo | R$/ano |
| Rt | Receita no ano t | R$ |
| Rtot | Total de receitas | R$ |
| Rtotm | Total de receitas do mês | R$ |
| Saldo | Saldo em caixa | R$ |
| Sbanco | Saldo bancário de uma conta (por conta j) | R$ |
| Sopc | Saldo da operação com investimentos | R$ |
| Sops | Saldo da operação sem investimentos | R$ |
| t | Ano do fluxo de caixa (índice, 0 a 10) | ano |
| tcom | Taxa de comissão (0,015; 0,03) | fração |
| TIR | Taxa interna de retorno | % a.a. |
| Vcab | Valor por cabeça | R$/cab |
| Venc | Vencimento de dívida (por ano a) | R$ |
| Vendas | Vendas totais do período | R$ |
| Vfr | Valor unitário do frete | R$/un. |
| Vmov | Valor da movimentação | R$ |
| VPL | Valor presente líquido | R$ |
| Aap | Área aproveitável | ha |
| Aben | Área de benfeitorias | ha |
| Aeco | Área de interesse ecológico | ha |
| Aeqa | Área equivalente da agricultura | ha |
| Aeqp | Área equivalente da pecuária | ha |
| Aimov | Área total do imóvel | ha |
| Aman | Área total ou de pasto manejada | ha |
| APP | Área de preservação permanente | ha |
| Aref | Área de reforma comprovada | ha |
| Aut | Área utilizada | ha |
| Cabfun | Cabeças por funcionário | cab/func |
| Ccam | Capacidade do caminhão | cab |
| Cmcomp | Custo médio de compra por cabeça (ou valor de avaliação) | R$/cab |
| Comp | Compras do evento ou período | cab |
| GEE | Grau de eficiência da terra | % |
| GMGC | Grau de maturidade em gestão do conhecimento | 0 a 100 |
| GUT | Grau de utilização da terra | % |
| Hafun | Hectares por funcionário | ha/func |
| ILZP | Índice de lotação da zona de pastagem | UA/ha |
| IPr | Índice de produtividade regional | unid./ha |
| Mj | Pontuação (média) da dimensão j | nota 1 a 5 |
| Nemb | Animais embarcados | cab |
| Nenv | Questionários efetivamente enviados | un. |
| Nj | Nota normalizada da dimensão j | 0 a 100 |
| nm | Número de meses com valor diferente de zero | meses |
| nq | Número de questões da dimensão | un. |
| Nresp | Respondentes do questionário | un. |
| Ocup | Ocupação do caminhão | % |
| Perdcam | Capacidade não usada do caminhão | % |
| Qcol | Quantidade colhida | unid. |
| qdim | Pontuação de uma questão da dimensão (por questão k) | nota 1 a 5 |
| RL | Reserva legal | ha |
| Sfim | Saldo final de cabeças do evento | cab |
| Sini | Saldo inicial de cabeças | cab |
| Sini1 | Saldo inicial de cabeças da primeira linha | cab |
| Sper | Saldo de cabeças do período | cab |
| Tano | Total anual | conforme a grandeza |
| Transf | Transferências, nascimentos e mortes, com sinal | cab |
| Tresp | Taxa de resposta | % |
| UAmed | UA total média de 12 meses | UA |
| Var | Variação percentual | % |
| Vend | Vendas do evento ou período | cab |
| Vf | Valor final | conforme a grandeza |
| Vi | Valor inicial | conforme a grandeza |
| Vmed | Média mensal | conforme a grandeza |
| Vmes | Valor do mês m (por mês) | conforme a grandeza |
| wj | Peso da dimensão j (soma 100) | % |
| Aext | Área do sistema extensivo | ha |
| Asist | Área do sistema intensivo | ha |
| E0 | Emissão de metano antes | kg CH4/animal |
| E1 | Emissão de metano depois | kg CH4/animal |
| Part | Participação da carne no valor do agronegócio | % |
| Pprod1 | Produtividade do produto 1 | kg/ha |
| Pprod2 | Produtividade do produto 2 | kg/ha |
| Rarea | Razão de área | adimensional |
| Redarea | Redução percentual de área | % |
| Redem | Redução percentual de emissão | % |
| Rprod | Razão de produtividade por área | adimensional |
| Vagro | Valor do agronegócio | US$ bi |
| Vcarne | Valor da carne | US$ bi |

## Desempenho e produção animal (DE)

```
(DE1)  GMD = (Pf − Pi) ÷ dias
(DE2)  ΔP = Pf − Pi
(DE3)  dias = ΔP ÷ GMD
(DE4)  Nmeses = dias ÷ DM
(DE5)  Pmed = (Pi + Pf) ÷ 2
(DE6)  dias_arr = KVA ÷ GMD
(DE7)  Pcarc = PV × RC
(DE8)  Narr = Pcarc ÷ KC
(DE9)  Narrv = PV ÷ KVA
(DE10) RGP = (PCf − PCi) ÷ ΔP × 100
(DE11) CA = CMSt ÷ Qprod
(DE12) Gáguas = (Pent − Pdesm) − Gseca
(DE13) GMDáguas = Gáguas ÷ dias_águas
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| DE1 | Ganho médio diário (GMD, ou ganho de peso diário) | Medir o desempenho de um animal ou lote entre duas pesagens, no mês, no ano ou no confinamento; comparar com a meta do plano nutricional e identificar animais abaixo do lote. | Pi e Pf: balança de curral, em jejum, mesmo horário (animal 1: 300 kg em janeiro e 460 kg em dezembro). dias: diferença real entre as datas de pesagem na caderneta (jan a dez = 334 dias). |
| DE2 | Peso a ganhar (ou ganho total) | Saber quanto o animal ainda precisa engordar até a venda, ou quanto ganhou no período. | Pi: pesagem na entrada ou nota de compra (180 kg). Pf: meta de venda (390 kg). |
| DE3 | Prazo em dias até o peso final | Calcular a duração do ciclo de recria ou engorda; GMD baixo alonga o prazo e os custos recorrentes. | ΔP calculado em DE2 (210 kg). GMD: pesagens sucessivas ou meta do plano nutricional (0,583 kg/dia). |
| DE4 | Prazo em meses | Converter o prazo em número de meses de custo recorrente (arrendamento, mão de obra, suplemento). | dias de DE3. DM: mês comercial (30). |
| DE5 | Peso médio do período | Obter o peso vivo médio entre a entrada e a saída para estimar consumo de suplemento. | Pi e Pf (200 e 390 kg, média 295 kg). |
| DE6 | dias para ganhar 1 arroba | Saber quantos dias o GMD exige para ganhar 30 kg de peso vivo, o equivalente de 1 @; base do custo operacional por @ produzida. | GMD: pesagens sucessivas (0,583 kg/dia, resulta em 51,46 dias). KVA: 30 kg de peso vivo por @. |
| DE7 | Peso de carcaça | Estimar o peso de carcaça a partir do peso vivo de venda. | PV: pesagem ou meta de venda (390 kg). RC: rendimento de carcaça pago pelo frigorífico (0,505). |
| DE8 | Arrobas de carcaça | Converter peso de carcaça em arrobas (1 @ = 15 kg de carcaça). | Pcarc de DE7 (196,95 kg). KC = 15 kg/@. |
| DE9 | Arrobas por peso vivo | Converter peso vivo em arrobas pela convenção de 30 kg de peso vivo por @ (compra de bezerros, ponto de equilíbrio, planejamento). | PV: pesagem (390 kg, resulta em 13 @). KVA = 30 kg/@. |
| DE10 | Rendimento do ganho de peso em carcaça | Medir quanto do ganho de peso vivo virou carcaça. | PCi e PCf: peso de carcaça inicial e final (kg), frigorífico. ΔP: ganho total de peso vivo (kg). |
| DE11 | Conversão alimentar | Medir a eficiência alimentar do confinamento, em kg de MS por @ (ou por kg) produzida. | CMSt: consumo total de MS do lote (kg), controle de trato. Qprod: arrobas ou kg produzidos, pesagem e romaneio. |
| DE12 | Ganho da recria nas águas | Separar o ganho da recria entre águas e seca. | Pdesm: peso à desmama (kg). Pent: peso de entrada no confinamento (kg). Gseca: ganho na seca (kg). Pesagens do rebanho. |
| DE13 | GMD da recria nas águas | Obter o ganho diário na época das águas. | Gáguas de DE12 (kg). dias_águas: dias do período (calendário). |

**Exemplo**
- DE1: (305 − 300) ÷ 31 = 0,1613 kg/dia no intervalo de janeiro a fevereiro; (460 − 300) ÷ 334 = 0,4790 kg/dia no ano.
- DE3 e DE4: ΔP = 390 − 180 = 210 kg; dias = 210 ÷ 0,583 = 360,2 dias; Nmeses = 360,2 ÷ 30 = 12,01 meses.
- DE6: 30 ÷ 0,583 = 51,46 dias por @.

**Observações**
- Duas convenções de arroba coexistem: 15 kg de carcaça (DE7 e DE8) ou 30 kg de peso vivo (DE9). A relação é vivo × 0,505 ÷ 15, que dá cerca de 29,7 kg de vivo por @, próxima de vivo ÷ 30 (equivale a rendimento de 50%). Escolha uma convenção por cálculo e não misture.
- Em DE6 os 30 kg são de ganho de peso vivo por arroba, não de carcaça.
- Se faltar a data de uma pesagem, usar 30 dias por intervalo mensal (330 dias entre a primeira e a última de 12 pesagens). Dividir o ganho anual por 365 subestima o GMD, porque 12 pesagens cobrem 11 intervalos: 160 ÷ 365 = 0,438 contra 160 ÷ 330 = 0,485 kg/dia.
- Em ciclos de duas fases (recria e engorda), o peso inicial da fase 2 é o peso final da fase 1 e o prazo total é a soma dos prazos das fases.
- Animal sem peso em algum mês gera ganho negativo ou exagerado no intervalo seguinte; confira as pesagens antes de interpretar o GMD.

## Reprodução e genética (RE)

```
(RE1)  Pmonta = 0,65 × Padulto
(RE2)  Dand = Dmi − 60
(RE3)  Ddiag = Dmf + 45
(RE4)  Dref = Ddose1 + 28
(RE5)  Dmi = Dchuvas + 30
(RE6)  Dmf = Dmi + 90
(RE7)  H = (X1 − X2) ÷ X2 × 100
(RE8)  Fcons = Rpar ÷ 2
(RE9)  ΔDEP = DEPA − DEPB
(RE10) Het = 100 × 0,5^g
(RE11) Fen = G + Amb + GxA
(RE12) Mpre = Nmort ÷ Nnasc × 100
(RE13) Trep = Ndesc ÷ Nfrep × 100
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| RE1 | Peso da novilha na monta | Definir quando a novilha pode entrar na estação de monta. | Padulto: peso adulto do rebanho (kg), pesagem das matrizes. |
| RE2 | Data do exame andrológico | Calendarizar o exame dos touros antes da monta (60 dias antes do início). | Dmi: data de início da monta (calendário reprodutivo). |
| RE3 | Data mínima do diagnóstico de gestação | Programar o diagnóstico de gestação no mínimo 45 dias após o fim da monta. | Dmf: data de fim da monta. |
| RE4 | Data do reforço vacinal | Programar o reforço 4 semanas (28 dias) após a primeira dose. | Ddose1: data da primeira dose (caderneta sanitária). |
| RE5 | Início da monta | Planejar a estação de monta: início cerca de 30 dias após o começo das chuvas. | Dchuvas: data de início das chuvas. |
| RE6 | Fim da monta | Planejar a estação de monta com duração de cerca de 90 dias. | Dmi de RE5. |
| RE7 | Heterose do cruzamento | Medir quanto a cria F1 supera a média dos pais. | X1: média da cria F1; X2: média dos pais, na mesma unidade da característica (por exemplo kg). |
| RE8 | Consanguinidade a partir do parentesco | Verificar o limite de parentesco para o acasalamento (avô comum). | Rpar: parentesco entre os pais (%), genealogia (6,25). |
| RE9 | Diferença entre touros pela DEP | Comparar dois touros pela diferença esperada na progênie. | DEPA e DEPB: DEP de cada touro, sumário de touros (+5 e −5, diferença de 10 kg). |
| RE10 | Heterozigose no cruzamento absorvente | Prever a heterozigose conforme as gerações de absorção. | g: número de gerações de cruzamento absorvente (0 a 6). |
| RE11 | Componentes do fenótipo | Separar o efeito do genótipo, do ambiente e da interação entre ambos. | G, Amb e GxA, na unidade da característica. |
| RE12 | Mortalidade pré-desmama | Medir a mortalidade de bezerros do nascimento à desmama. | Nmort: bezerros mortos até a desmama; Nnasc: bezerros nascidos; controle de manejo. |
| RE13 | Taxa de reposição de matrizes | Medir a reposição de matrizes do rebanho de cria. | Ndesc: matrizes descartadas; Nfrep: fêmeas em reprodução; controle do rebanho. |

**Exemplo**
- RE8: parentesco de 6,25% por avô comum equivale a 3,125% de consanguinidade.
- RE10: 100, 50, 25, 13, 6, 3 e 2 % da geração 0 à geração 6.
- RE9: com DEP de +5 e −5 kg, a diferença é de 10 kg.

**Observações**
- Em cruzamento rotacionado a heterose estável é de 67% com 2 raças e cerca de 86% com 3 raças.
- RE2 a RE6 são deslocamentos em dias sobre uma data de calendário; o diagnóstico (RE3) pode ser feito depois da data mínima, nunca antes.
- RE8 vale como limite prático: acima do parentesco de avô comum, evitar o acasalamento.

## Nutrição, suplementação e confinamento (NU)

```
(NU1)  CMS = −0,6273 + 0,06453 × PV^0,75 + 3,871 × GMD − 0,614 × GMD^2
(NU2)  Cons = PV × fPV
(NU3)  MNing = CMS × fing ÷ MSing
(NU4)  Eing = MNing × dias × Ncab
(NU5)  Qlote = Cons × Ncab
(NU6)  CMSt = CMS × dias × Ncab
(NU7)  Trato_novo = Trato + s × Trato × 0,005
(NU8)  Ração_novo = Ração_ant × (1 + aj)
(NU9)  CMScalor = CMS × (1 − rcalor)
(NU10) Ced = CkgMS × red
(NU11) Econ_dieta = CMSt × Ced
(NU12) Ckg = Psaco ÷ Ksaco
(NU13) Ccons = Cons × Ckg
(NU14) Cmês = Ccons × DM
(NU15) Cper = Cmês × Nmeses
(NU16) Csup = Ccons × DM × Nmeses × Ncab
(NU17) Nsacos = Qlote ÷ Ksaco
(NU18) mi = Pbatida × pcti ÷ 100
(NU19) Spct = Σ pcti
(NU20) Ktotal = Pbatida × Spct ÷ 100
(NU21) Pacum_i = Pacum_(i−1) + mi
(NU22) Cing_i = mi × Ckg_i
(NU23) Cbatida = Σ Cing_i
(NU24) Cmist = Cbatida ÷ Ktotal
(NU25) Csaco = Cmist × Ksaco
(NU26) Eleite = 0,75 × Mleite
(NU27) Dener = Nener − Eleite
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| NU1 | Consumo de MS de animais cruzados | Estimar o consumo de matéria seca para formular a dieta de animais cruzados. | PV: peso corporal (kg), balança. GMD: ganho previsto (kg/dia). Exemplo: 360 kg e 1,4 kg/dia resultam em 8,92 kg de MS/dia. |
| NU2 | Consumo diário por percentual do peso | Estimar o consumo de MS da dieta (2,3 a 2,8% do PV no confinamento) ou a dose diária de suplemento por cabeça (0,1% do peso médio). | PV (kg; para suplemento use Pmed de DE5): balança (295 kg). fPV: fração do peso vivo, nutricionista ou rótulo (0,0235 na dieta; 0,001 no suplemento). |
| NU3 | Matéria natural do ingrediente | Converter a MS do ingrediente em matéria natural para o trato. | CMS (kg MS/animal/dia). fing: fração do ingrediente na MS da dieta, formulação. MSing: fração de MS do ingrediente, análise bromatológica. |
| NU4 | Estoque do ingrediente | Calcular quanto comprar e armazenar de cada ingrediente (dividir por 1.000 para toneladas). | MNing de NU3. dias: dias de confinamento. Ncab: animais no confinamento. |
| NU5 | Quantidade diária do lote | Calcular o trato diário do lote (dieta ou suplemento) antes do ajuste por leitura de cocho. | Cons (kg/animal/dia) e Ncab: contagem do lote (14,72 kg × 100 animais = 1.472 kg MN/dia; 0,3 kg × 100 = 30 kg/dia). |
| NU6 | Consumo total de MS do lote | Calcular a necessidade total de MS do lote no confinamento. | CMS (kg/dia), dias, Ncab. |
| NU7 | Ajuste do trato pela nota de cocho (percentual pequeno) | Corrigir o trato do dia com a nota de leitura de cocho de 1 a 5. | Trato: trato anterior (kg MN). s: +1 para nota 1 ou 2 (aumenta), −1 para nota 4 ou 5 (reduz), 0 para nota 3. |
| NU8 | Ajuste da ração pela escala de cocho | Ajustar a ração do dia pela leitura de cocho com escores de +2 a −2. | Ração_ant: ração do dia anterior (kg). aj: +0,10; +0,05; 0; −0,05; −0,10 conforme o escore +2 a −2. |
| NU9 | Consumo de MS sob calor | Prever a queda de consumo em dias quentes. | CMS (kg/dia). rcalor: redução de 0,10 a 0,35 acima de 35 °C. |
| NU10 | Economia por kg de MS | Obter a economia por kg de MS com a redução do custo da dieta. | CkgMS: custo da dieta (R$/kg MS), contabilidade (0,45). red: redução percentual do custo (0,05). Ou informe diretamente Ced (R$ 10/t = 0,01 R$/kg). |
| NU11 | Economia da dieta no lote | Estimar quanto vale uma redução do custo da dieta para todo o confinamento. | CMSt de NU6. Ced de NU10. |
| NU12 | Custo do kg do suplemento ou do ingrediente | Comparar suplementos e ingredientes pelo preço por kg. | Psaco: preço do saco, nota fiscal (R$ 52,50). Ksaco: kg por saco, rótulo (30). Vale para cada ingrediente i. |
| NU13 | Custo diário por animal | Obter o custo diário de suplemento por cabeça. | Cons de NU2 (se medido, use a pesagem de sobras no cocho: 0,15 kg/animal/dia). Ckg de NU12. |
| NU14 | Custo mensal por animal | Obter o custo mensal de suplemento por cabeça. | Ccons de NU13. DM = 30. |
| NU15 | Custo de suplemento no período | Obter o gasto total de suplemento por animal até o peso final, válido se GMD e consumo se mantiverem. | Cmês de NU14. Nmeses de DE4. |
| NU16 | Custo de suplementação do ciclo | Obter o desembolso de suplemento do lote no ciclo. | Ccons, Nmeses, Ncab. Em dois suplementos (recria e engorda), some o custo de cada fase. |
| NU17 | Número de sacos por dia | Planejar compra e logística do suplemento. | Qlote de NU5. Ksaco (30). Na prática arredonde para cima. |
| NU18 | Quantidade do ingrediente na batida | Montar a receita em kg a partir das percentagens. | Pbatida: peso da batida (kg), tamanho da misturadora (1.000). pcti: percentagem do ingrediente na batida (%), nutricionista (milho 40, soja 5). |
| NU19 | Soma das percentagens | Conferir que a receita soma 100% antes de confiar no custo. | pcti de todos os ingredientes. |
| NU20 | Peso total da batida | Conferir o peso total da mistura. | Pbatida, Spct. |
| NU21 | Peso acumulado | Conferir a sequência de pesagem dos ingredientes na misturadora. | mi de NU18. |
| NU22 | Custo do ingrediente na batida | Ver o peso de cada ingrediente no custo (o núcleo costuma dominar). | mi de NU18. Ckg_i de NU12. |
| NU23 | Custo total da batida | Obter o custo da batida inteira. | Cing_i de NU22. |
| NU24 | Custo por kg da mistura | Comparar com outro suplemento ou produto pronto. | Cbatida de NU23. Ktotal de NU20. |
| NU25 | Custo do saco da mistura | Comparar com o produto comercial e alimentar o custo por animal. | Cmist de NU24. Ksaco (30). |
| NU26 | Energia fornecida pelo leite ao bezerro | Calcular a energia que o leite supre ao bezerro. | Mleite: consumo de leite (kg/dia); bezerro de 1 a 2 meses requer 4,4 a 6,8 kg/dia. Leite: 0,75 Mcal/kg. |
| NU27 | Déficit energético do bezerro a pasto | Calcular quanto falta suprir por pasto ou suplemento. | Nener: necessidade total (Mcal ED/dia), tabela de exigências. Eleite de NU26. |

**Exemplo**
- NU1: −0,6273 + 0,06453 × 360^0,75 + 3,871 × 1,4 − 0,614 × 1,4^2 = 8,92 kg MS/dia.
- NU11: 10.000 animais × 460 kg × 0,0235 × 95 dias = 10.269.500 kg de MS; com Ced = 0,45 × 0,05 = R$ 0,0225/kg a economia é de cerca de R$ 231.000 (reportado como R$ 230.000).
- NU11 com R$ 10/t de MS poupados: 10.000 × 450 × 0,0235 × 95 = 10.046.250 kg; × 0,01 R$/kg = R$ 100.462,50.
- NU12 a NU15: saco de R$ 79 ÷ 30 kg = R$ 2,6333/kg; consumo de 0,295 kg/dia custa R$ 0,7768/dia, R$ 23,305/mês; em 8,44 meses são R$ 196,80 por animal.
- NU18 a NU25: batida de 1.000 kg com milho 400 kg × R$ 1,2667 + soja 50 kg × R$ 1,80 + trigo 150 kg × R$ 0,75 + núcleo 350 kg × R$ 5,20 + sal 50 kg × R$ 0,80 = R$ 2.569,17; R$ 2,5692/kg; saco de 30 kg a R$ 77,07.
- NU18 a NU25 em ração de 500 kg (milho 83,5%, soja 12%, núcleo 3%, ureia 1,5%): R$ 1.000,55; R$ 2,0011/kg; saco de 30 kg a R$ 60,03.
- NU17: 100 animais × 300 kg × 0,001 = 30 kg/dia, 1 saco de 30 kg por dia.
- NU2 e NU13: saco de R$ 52,50 de 30 kg = R$ 1,75/kg; Pmed = (180 + 390) ÷ 2 = 285 kg; Cons = 285 × 0,001 = 0,285 kg/dia; Ccons = R$ 0,49875/dia.
- NU13 a NU15 (13 bezerras, 180 a 390 kg, GMD 0,583): Nmeses = 210 ÷ 0,583 ÷ 30 = 12,01; suplementação do lote = 0,49875 × 30 × 12,01 × 13 = R$ 2.335,48. Com GMD 0,75 são 40 dias por @ e R$ 31,07/@ de suplemento (EC1).
- NU18 a NU25 com ração de 500 kg: milho 417,5 kg × R$ 1,80 = R$ 751,50; soja 60 × 2,695 = R$ 161,70; núcleo 15 × 3,7333 = R$ 56,00; ureia 7,5 × 4,18 = R$ 31,35. Preços de saco: milho 72 (40 kg), soja 107,80 (40 kg), núcleo 112 (30 kg), ureia 209 (50 kg). Com 0,15 kg/dia, 50 dias por @ e 350 dias: R$ 0,3002/dia, R$ 15,01/@, R$ 105,06 no período.
- NU18 a NU25 em outras misturas: proteico-energético com soja (70/5/25) R$ 2,0983/kg e saco de R$ 62,95; com DDG (65/10/25) R$ 2,0933/kg; com farelo de trigo (60/20/20) R$ 1,9890/kg.

**Observações**
- Duas convenções para o percentual do peso: na dieta de confinamento usa-se 2,3 a 2,8% do PV em MS (fPV de 0,023 a 0,028); na suplementação o fornecimento típico é 0,1% do peso vivo médio (1 g por kg de PV), extremo inferior da dose de 1 a 3 g/kg de PV recomendada na seca.
- NU11 também pode partir de CMS obtido por NU2 (PV × fPV): 10.000 × 460 × 0,0235 × 95 × (0,45 × 0,05) ≈ R$ 230.000.
- NU3 parte do consumo de MS da dieta; NU18 parte do tamanho da batida. São bases diferentes, não misture.
- NU7 e NU8 são duas convenções de ajuste: pequena correção percentual por nota de cocho, ou variação de 5 a 10% por escore. Use uma delas de forma consistente.
- Proporção usual de silagem: 65% de massa verde (capim) e 35% de grãos (milho, soja).
- Sobra aceita no cocho de 3% sobre a dieta total custa cerca de R$ 138.000 em 10.000 animais (valor reportado, em um cenário de confinamento de 95 dias).
- As proporções digitadas na batida não são validadas: confira Σ pcti = 100 em NU19.
- Não há balanceamento nutricional (PB, NDT, EM) nas receitas: o custo por kg é apenas comparação de preço, não de equivalência nutricional. Valide a segurança do uso de ureia na referência de nutrição antes de formular.

## Pastagem, lotação e adubação (PA)

```
(PA1)  Ptotal = Ncab × Pmed
(PA2)  UA = Ptotal ÷ PUA
(PA3)  UAcat = Cabm × icv
(PA4)  LotUA = UA ÷ Apast
(PA5)  Lotcab = Ncab ÷ Apast
(PA6)  PrPVha = GPV ÷ Apast
(PA7)  PrPVha = Lotcab × Gcab
(PA8)  Pcarcha = Narrha × KC
(PA9)  Apast = Ncab ÷ Lalvo
(PA10) Tpiq = Atot ÷ Npiq
(PA11) Atot = Npiq × Tpiq ÷ 10.000
(PA12) Npiq = PD ÷ PO + 1
(PA13) Ksem = SPVha × 100 ÷ VC
(PA14) Ndose = MS × 0,015 − NMO
(PA15) Ureia = Ndose ÷ 0,45 ÷ efur
(PA16) ΔMScal = (MScom − MSsem) × (1 − 0,20)
(PA17) Rcal = (Gcal − Ccalc) ÷ Ccalc
(PA18) SSP = 50 × ΔPsolo
(PA19) P2O5 = kP × Arg
(PA20) Estac = MSseca ÷ MStot × 100
(PA21) Lotalta = Rlot × Lotmed
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| PA1 | Peso total do lote | Obter o peso vivo total para converter em unidades animais. | Ncab: contagem do rebanho (500). Pmed: pesagem de amostra do lote (355 kg). |
| PA2 | Unidades animais do lote | Converter o lote em UA (1 UA = 450 kg de PV). | Ptotal de PA1. PUA: peso de 1 UA, convenção adotada (450 kg). |
| PA3 | UA por categoria | Converter o rebanho por categoria em UA para lotação. | Cabm: média mensal de cabeças da categoria, controle de rebanho. icv: índice de conversão da categoria em UA. |
| PA4 | Taxa de lotação em UA/ha | Comparar com a capacidade de suporte da forrageira e decidir lotar, desfazer ou reforçar. | UA de PA2. Apast: só a área efetivamente pastejada (ha), mapa ou georreferenciamento (137 ha). |
| PA5 | Taxa de lotação em cabeças/ha | Comparar a carga animal com médias regionais e nacionais. | Ncab, Apast (221 milhões de cabeças ÷ 165 milhões de ha = 1,34 cab/ha). |
| PA6 | Produtividade pecuária por área | Medir o ganho de peso vivo por hectare e ano. | GPV: ganho de PV do rebanho no ano (kg), pesagens. Apast. |
| PA7 | Produtividade por lotação e ganho individual | Calcular kg de PV/ha a partir da lotação e do ganho por cabeça (integração lavoura-pecuária). | Lotcab de PA5. Gcab: ganho por cabeça no ano (kg). Exemplo: 3 cab/ha × 179 kg = 537 kg PV/ha. |
| PA8 | Produtividade em carcaça | Comparar a produtividade em kg de carcaça por hectare. | Narrha: @/ha.ano produzidas (5,57 @/ha). KC = 15. |
| PA9 | Área do lote | Dimensionar a área para o lote na lotação desejada. | Ncab. Lalvo: lotação alvo (2 cab/ha, projeto). |
| PA10 | Tamanho do piquete | Dividir a área do pastejo rotacionado pelo número de piquetes. | Atot: área total (m²) ou (ha). Npiq: número de piquetes de PA12 (93.000 m² ÷ 31 = 3.000 m²). |
| PA11 | Área total dos piquetes | Obter a área total ocupada pelos piquetes. | Npiq: nº de piquetes (24). Tpiq: área de cada piquete (m², 2.167). |
| PA12 | Número de piquetes do rotacionado | Dimensionar o pastejo rotacionado. | PD: período de descanso (dias). PO: período de ocupação (dias), manejo da forrageira. |
| PA13 | Taxa de semeadura corrigida | Corrigir a quantidade de sementes pelo valor cultural. | SPVha: kg de semente pura viável por ha, recomendação (360 ou 500). VC: valor cultural (%), etiqueta da semente. |
| PA14 | Dose de nitrogênio | Calcular a dose de N pelo balanço entre o extraído pela forragem e o fornecido pela matéria orgânica. | MS: produção de MS (kg/ha.ano, 15.000). NMO: N fornecido pela MO do solo (100 kg/ha com 2% de MO), análise de solo. |
| PA15 | Ureia necessária | Converter a dose de N em ureia. | Ndose de PA14 (125 kg N/ha). Teor de N da ureia 45%. efur: eficiência de aproveitamento do N (0,50). |
| PA16 | Ganho de MS pela calagem | Avaliar o efeito da calagem na produção de forragem. | MScom e MSsem: produção de MS com e sem calagem (t/ha.ano), ensaio. |
| PA17 | Retorno econômico da calagem | Avaliar o retorno do investimento em calcário. | Gcal: ganho em R$ com a MS adicional. Ccalc: custo do calcário (R$). |
| PA18 | Superfosfato na fosfatagem | Dimensionar o superfosfato simples (eficiência 50%). | ΔPsolo: aumento desejado de P no solo (mg/dm³), análise de solo. |
| PA19 | P2O5 por teor de argila | Dimensionar o P2O5 conforme a argila. | Arg: argila do solo (%), análise de solo. kP: 3 a 5 kg P2O5/ha por 1% de argila. |
| PA20 | Estacionalidade da produção | Medir a estacionalidade da produção de forragem. | MSseca: MS produzida na seca; MStot: MS total anual (kg/ha). |
| PA21 | Lotação de fazendas de alto desempenho | Estimar a lotação de referência de fazendas de alto desempenho a partir da média geral (valor derivado). | Rlot: razão entre a lotação de alto desempenho e a média geral (3,12). Lotmed: lotação média geral (1,21 cab/ha). |

**Exemplo**
- PA2 e PA4: 500 × 355 = 177.500 kg; UA = 177.500 ÷ 450 = 394,44; taxa = 394,44 ÷ 137 = 2,88 UA/ha; em cabeças, 500 ÷ 137 = 3,65 cab/ha.
- PA8: 5,57 @/ha × 15 = 83,55 kg de carcaça/ha.
- PA14 e PA15: 15.000 × 0,015 − 100 = 125 kg N/ha; ureia = 125 ÷ 0,45 ÷ 0,50 = 555,6 kg/ha.
- PA11: 24 × 2.167 m² = 52.008 m² = 5,2 ha.
- PA21: 3,12 × 1,21 = 3,78, cerca de 3,8 cab/ha.

**Observações**
- PA4 (UA/ha) e PA5 (cab/ha) são grandezas diferentes: não rotule UA/ha como cabeças por hectare.
- A convenção 1 UA = 450 kg de PV = 15 @ equivale a 30 kg de PV por @.
- Referência de lotação no Pantanal: 0,33 cab/ha.
- Em PA13 a dose de sementes pode ser lida como 360 ÷ VC ou 500 ÷ VC (em %).
- Em PA16 o fator 0,20 reduz o ganho bruto de MS antes de valorar.
- A projeção de lotação nacional de 1,25 para 1,39 cab/ha (+8,6 milhões de ha convertidos) é uma variação percentual simples (GE19).
- Em PA10 e PA11 mantenha m² e ha em unidades coerentes (1 ha = 10.000 m²).

## Leite e qualidade (LE)

```
(LE1)  Vtanque = 2,5 × Qlote
(LE2)  ρ = mam ÷ Vam
(LE3)  N = N0 × 2^(tref ÷ 15)
(LE4)  PerdaCCS = Nvac × 4
(LE5)  Lano = Nvac × Lvd × 365
(LE6)  Lalq = Lano ÷ Nalq
(LE7)  Lha = Lalq ÷ 2,4
(LE8)  Lha = Qmod × 365 ÷ Aprod
(LE9)  Plact = DM × Σ Lmd
(LE10) Pvl = PL ÷ IP × 100
(LE11) Δclim = (Pdep − Pant) ÷ Pant × 100
(LE12) Cord = Pord × Litros
(LE13) Cbez = Ldb × dalim × Pleite
(LE14) Rleite = Lha × Pleite
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| LE1 | Capacidade do tanque de expansão | Dimensionar o tanque com folga para o crescimento da produção. | Qlote: produção diária atual e futura (L), controle leiteiro (500 L/dia resulta em 1.250 L). |
| LE2 | Densidade do leite | Conferir a densidade (1,028 a 1,034 kg/L) como teste de qualidade. | mam: massa da amostra (kg). Vam: volume da amostra (L). |
| LE3 | Crescimento bacteriano | Estimar a multiplicação das bactérias do leite fora de refrigeração (dobra a cada 15 minutos em temperatura ambiente). | N0: contagem inicial. tref: tempo fora da refrigeração (min). |
| LE4 | Perda de produção por CCS alta | Estimar a perda diária de produção do rebanho com contagem somática (CCS) elevada. | Nvac: vacas em lactação. CCS do tanque (laudo); perda de cerca de 4 L/vaca/dia. |
| LE5 | Produção anual do rebanho | Calcular a produção anual do rebanho em lactação. | Nvac, Lvd: litros por vaca por dia (controle leiteiro). |
| LE6 | Produtividade por alqueire | Calcular litros por alqueire por ano. | Lano de LE5. Nalq: número de alqueires da área de leite. |
| LE7 | Produtividade por hectare a partir do alqueire | Converter litros por alqueire em litros por hectare. | Lalq de LE6 (2,4 ha por alqueire). |
| LE8 | Litros por hectare do módulo | Calcular a produtividade de leite por área produtiva do módulo. | Qmod: litros por dia do módulo. Aprod: área produtiva (ha). |
| LE9 | Produção da lactação | Calcular a produção total da lactação a partir das médias diárias mensais. | Lmd: produção média diária de cada mês (L/dia), controle leiteiro. |
| LE10 | Percentual de vacas em lactação | Calcular a proporção do rebanho em lactação a partir do período de lactação e do intervalo entre partos. | PL: período de lactação (meses). IP: intervalo entre partos (meses). |
| LE11 | Ganho de produção por climatização | Comparar a produção com e sem sombra, ventilação ou aspersão. | Pant e Pdep: kg de leite por vaca por ordenha antes e depois. |
| LE12 | Custo da ordenha manual | Estimar o custo da ordenha manual. | Litros: litros ordenhados (L). Pord: custo da ordenha por litro (R$ 0,17/L). |
| LE13 | Leite consumido pelos bezerros | Estimar o valor do leite aleitado aos bezerros. | Ldb: litros por dia por bezerro (3,5). dalim: dias de aleitamento (60). Pleite: preço do leite (R$ 0,90/L). |
| LE14 | Renda bruta de leite por hectare | Estimar a renda bruta do leite por área. | Lha de LE7 ou LE8 (5.794 L/ha.ano). Pleite: preço do leite (R$ 0,90/L). |

**Exemplo**
- LE1: 500 L/dia × 2,5 = 1.250 L.
- LE3: em 1 h (60 min) são 2^4 = 16 vezes; em 3 h são 2^12 = 4.096 vezes.
- LE11: sombra e ventilação, (19,19 − 18,20) ÷ 18,20 = +5,4%; com aspersão, (20,53 − 18,20) ÷ 18,20 = +12,8%.
- LE13: 3,5 × 60 × 0,90 = R$ 189 por bezerro.
- LE14: 5.794 × 0,90 = R$ 5.215 por hectare por ano.

**Observações**
- Valor da CCS: perda de 30% da produção com 1,5 milhão CS/mL; o rendimento de queijo cai 11% com 900.000 CS/mL em relação a 100.000 CS/mL.
- Índice crioscópico máximo: −0,530 °H equivale a −0,512 °C.
- Verminose pode reduzir até 20% do leite (cerca de 40 kg por animal por ano).
- LE7 e LE8 dão o mesmo resultado (L/ha.ano) por caminhos diferentes; use a que corresponder aos dados disponíveis.
- LE9 usa mês de 30 dias.
- Uma tabela de referência traz 6.640 L/ha.ano e R$ 4.978/ha.ano para um mesmo sistema; as fórmulas LE8 e LE14 não reproduzem esses valores e a diferença não é explicada.

## Sanidade, manejo e bem-estar (SA)

```
(SA1)  Dcl = D1000 × (Vres ÷ 1.000) × Calvo
(SA2)  Vprod = Vág × dprod
(SA3)  mdet = 10 × Vsol
(SA4)  Ddose = ddose × PV
(SA5)  Larvas = 5.000 × Nfem
(SA6)  PerdaVerm = pverm × Qleite
(SA7)  Prev = Nsinais ÷ Nobs × 100
(SA8)  Asombra = asombra × Ncab
(SA9)  Rbaia = Naloj ÷ Nbaias
(SA10) Nhig = 0,70 × Nvac
(SA11) Ncasco = 0,25 × Nvac
(SA12) Qágua = Ncab × qágua
(SA13) Nhem = 0,2 × MAX(0; hviag − 6)
(SA14) Pdesc = 0,42 × hdesc
(SA15) Pjej = pjej × PV
(SA16) Econ_san = Csan_antes − Csan_dep
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| SA1 | Dose de cloro para o reservatório | Ajustar a dose de cloro ao volume do reservatório e ao teor desejado. | D1000: dose para 1 mg/L em 1.000 L, rótulo (água sanitária 2%: 50 mL; hipoclorito 20 a 30%: 30 a 50 mL; cloro granulado 65%: 1,54 g). Vres: volume (L). Calvo: teor desejado (mg/L, 0,5). |
| SA2 | Solução de sanitização e limpeza | Preparar solução de sanitização de latões, baldes e utensílios, ou de limpeza do reservatório. | Vág: volume de água (L). dprod: 10 mL/L de água sanitária ou 2 mL/L de hipoclorito. |
| SA3 | Detergente alcalino | Preparar o detergente para limpeza de utensílios. | Vsol: volume de solução (L); 20 g de pó para 2 L (10 g/L). |
| SA4 | Dose total de medicamento | Calcular a dose de cada animal a partir da dose por kg de peso vivo. | ddose: dose por kg de PV (tabela do produto). PV: peso vivo (kg), balança. |
| SA5 | Larvas evitadas por retirada de fêmeas ingurgitadas | Estimar o ganho sanitário da retirada manual de fêmeas ingurgitadas de carrapato. | Nfem: fêmeas retiradas por dia. |
| SA6 | Perda máxima por verminose | Estimar o impacto máximo da verminose no leite. | Qleite: produção de leite do rebanho (L ou kg no período), controle leiteiro. pverm: fração máxima perdida (0,20). |
| SA7 | Prevalência de estresse térmico | Observar o lote e decidir a intervenção no ambiente. | Nsinais: vacas com sinais de estresse; Nobs: vacas observadas, contagem no pátio. |
| SA8 | Área mínima de sombra | Dimensionar a sombra do lote. | Ncab: animais do lote. asombra: m² por animal (2 para vaca adulta em lactação; 6 como parâmetro geral). |
| SA9 | Razão vacas por baia | Verificar superlotação do galpão (meta 1,0). | Naloj: vacas alojadas. Nbaias: número de baias. |
| SA10 | Vacas a avaliar no escore de higiene | Definir a amostra do escore de higiene (70% das vacas). | Nvac: vacas do lote. |
| SA11 | Vacas a avaliar no escore de casco | Definir a amostra mínima do escore de casco (25% do lote). | Nvac: vacas do lote. |
| SA12 | Consumo de água do lote | Dimensionar reservatório, bebedouros e oferta de água. | Ncab. qágua: 50 a 60 L/animal/dia (mínimo 50). |
| SA13 | Hematomas por viagem | Estimar hematomas na carcaça conforme a duração do transporte. | hviag: horas de viagem; acima de 6 h o acréscimo é de 0,2 hematoma por carcaça por hora. |
| SA14 | Perda de peso no descanso | Estimar a perda de peso no descanso pós-viagem. | hdesc: horas de descanso (por exemplo 16). |
| SA15 | Perda de peso no jejum do frigorífico | Estimar a perda de peso no abate. | PV: peso de embarque. pjej: 0,0276 a 0,0418. |
| SA16 | Economia sanitária por cabeça | Comparar custo sanitário antes e depois de uma mudança de manejo. | Csan_antes e Csan_dep: custo sanitário por cabeça (R$/cab), controle sanitário (15 e 2). |

**Exemplo**
- SA1: 0,5 mg/L em 2.000 L com água sanitária 2%: 50 × (2.000 ÷ 1.000) × 0,5 = 50 mL.
- SA2: 100 mL de água sanitária (ou 20 mL de hipoclorito) para 10 L de água.
- SA12: 100 animais × 50 L = 5.000 L/dia.
- SA16: 15 − 2 = R$ 13 por cabeça.

**Observações**
- Doses de antibiótico na dermatofilose: dose única de penicilina 70.000 UI/kg PV e estreptomicina 70 mg/kg PV; dose diária por 5 dias de penicilina 5.000 UI/kg PV, estreptomicina 5 mg/kg PV e oxitetraciclina 20 mg/kg PV (surtos).
- Água clorada para tetos: 5 L de água com cloro comercial (2 a 20% de hipoclorito de sódio); o volume de cloro depende da concentração do produto, sem fórmula única.
- Frequência respiratória acima de 80 movimentos do flanco por minuto indica estresse. Intervir no ambiente quando a prevalência (SA7) for igual ou maior que 70%.
- O escore de casco deve cobrir pelo menos 25% do lote (SA11).
- A perda por morbidade no confinamento foi estimada em cerca de R$ 30.000 (5% de 10.000 animais com −0,1 kg/dia, 95 dias, rendimento de ganho de 70%, @ a R$ 132); valor reportado.
- Mínimos de bem-estar: água de pelo menos 50 L/animal/dia e sombra de pelo menos 6 m²/animal no manejo geral (2 m² por vaca adulta em lactação).

## Instalações e dimensionamento (IN)

**Cerca de arame liso (por km)**

```
(IN1) Postes = arredondar_cima(Mkm ÷ Esp) + 1
(IN2) MOkm = Varr × Akm
(IN3) Ckm = Pposte × Postes + Prolo × Rolkm × Fios + Pest × Nest + MOkm
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| IN1 | Postes por km | Orçar a quantidade de postes de uma cerca; +1 é o poste de ponta | Metros por km (m), projeto da cerca (1.000); distância entre postes (m), padrão do empreiteiro (8) |
| IN2 | Mão de obra por km | Custo da empreitada quando o preço local é cotado em @ de vaca | Valor da @ de vaca (R$/@), preço local da empreitada (220); @ por km (12) |
| IN3 | Custo por km de cerca | Referência de custo para qualquer extensão; comparar com a proposta da empreiteira | Preço do poste (R$/un.) (30); rolos de arame por km (1) e preço do rolo (R$) (600); nº de fios (5); esticadores por km (3) e preço do esticador (R$) (250) |

**Cerca de arame liso (obra completa)**

```
(IN4) Mcerca = km × Mkm
(IN5) Ptot = Postes × km
(IN6) Cpostes = Ptot × Pposte
(IN7) Rolot = Rolkm × km
(IN8) Carame = Rolot × Fios × Prolo
(IN9) Etot = Nest × km
(IN10) Cest = Etot × Pest
(IN11) Cmocerca = Nmo × km × MOkm
(IN12) Ccerca = Cpostes + Carame + Cest + Cmocerca
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| IN4 | Metros totais de cerca | Extensão total a construir | Quilômetros a executar (km), medição no croqui ou mapa (1) |
| IN5 | Postes totais | Quantidade de postes da obra | Postes por km (IN1); km |
| IN6 | Custo dos postes | Parcela de postes no orçamento | Postes totais (IN5); preço do poste |
| IN7 | Rolos de arame totais | Quantidade de rolos da obra | Rolos por km; km |
| IN8 | Custo do arame | Parcela de arame no orçamento | Rolos totais (IN7); nº de fios; preço do rolo |
| IN9 | Esticadores totais | Quantidade de esticadores | Esticadores por km; km |
| IN10 | Custo dos esticadores | Parcela de esticadores | Esticadores totais (IN9); preço do esticador |
| IN11 | Custo de mão de obra da cerca | Parcela de mão de obra | Quantidade de mão de obra por km (un.) (1); km; mão de obra por km (IN2) |
| IN12 | Custo total da cerca | Desembolso total da obra | Parcelas IN6, IN8, IN10 e IN11 |

**Exemplo**

- 1 km com postes a cada 8 m: 126 postes × R$ 30 = R$ 3.780; arame 5 fios × R$ 600 = R$ 3.000; 3 esticadores × R$ 250 = R$ 750; mão de obra 12 × 220 = R$ 2.640; total R$ 10.170 por km (sem o poste de ponta, 125 postes e R$ 10.140).

**Cocho, bebedouro, curral e alojamento**

```
(IN13) Lcocho = mcab × Ncab
(IN14) Lcreep = lcreep × Nbez
(IN15) Acreep = 1,5 × Nbez
(IN16) Lbeb = lbeb × Ncab
(IN17) Nbeb = 0,15 × Ncab
(IN18) Nbsim = 0,10 × Naloj
(IN19) Ncub = 1,05 × Ncab
(IN20) Aembute = 2 × Aser
(IN21) Rpatio = 2 × Cveic
(IN22) Arem = 4 × Ncab
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| IN13 | Metros de cocho do lote | Dimensionar o cocho linear de suplementação | Cocho por cabeça (m/cab), projeto da fazenda (0,10); nº de cabeças (50) |
| IN14 | Cocho linear do creep | Dimensionar o cocho do creep-feeding | Cocho por bezerro (m) (0,10 = 10 cm); nº de bezerros |
| IN15 | Área do creep | Dimensionar a área do creep-feeding | Nº de bezerros |
| IN16 | Bebedouro linear | Dimensionar o comprimento do bebedouro do lote | Espaço linear por animal (m) (0,10); nº de animais |
| IN17 | Animais bebendo ao mesmo tempo | Dimensionar o bebedouro pela fração simultânea do lote (15%) | Nº de animais do lote |
| IN18 | Bebedouros simultâneos no alojamento | Dimensionar bebedouros de animais alojados (10%) | Nº de animais alojados |
| IN19 | Número de cubículos | Dimensionar cubículos com 5% a mais que os animais | Nº de animais |
| IN20 | Área do embute | Dimensionar o embute do curral | Área da seringa (m²) |
| IN21 | Raio do pátio do embarcadouro | Dimensionar o pátio de manobra do embarcadouro | Comprimento do maior veículo (m) |
| IN22 | Área da remanga | Dimensionar a remanga | Nº de animais |

**Piquetes de confinamento**

```
(IN23) Npiqc = Ncab ÷ 100
(IN24) Apiq = Napiq × aanim
(IN25) Cpiq = Napiq × ecocho + Cport
(IN26) Wpiq = Apiq ÷ Cpiq
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| IN23 | Número de piquetes | Dimensionar o confinamento com no máximo 100 animais por piquete | Nº de animais |
| IN24 | Área do piquete | Dimensionar a área de cada piquete | Animais por piquete (Napiq); área por animal (m²) (8 a 12) |
| IN25 | Comprimento do piquete | Dimensionar o comprimento (cocho mais porteira) | Animais por piquete; espaço de cocho por animal (m); comprimento da porteira (m) |
| IN26 | Largura do piquete | Obter a largura a partir da área e do comprimento | Área e comprimento do piquete (1.000 ÷ 73,5 = 13,6 m) |

**Observações**

- Na cerca, o poste de emenda é contado duas vezes quando há vários quilômetros (superestimativa leve); porteiras, mata-burros, grampos, cantos e frete não entram.
- A @ de referência da mão de obra da cerca é a de vaca, não a de boi gordo.
- Para o cocho do lote há duas referências: 0,05 m e 0,10 m (10 cm) por cabeça; adotar a do projeto.

## Custos e economia (EC)

**Suplemento por arroba produzida**

```
(EC1) Car = Ccons × dias_arr
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC1 | Custo do suplemento por arroba produzida | Comparar com o preço da @ e entre produtos; menor R$/@ não é necessariamente o maior retorno | Ccons de NU13; dias_arr de DE6 |

**Diária e custo da arroba**

```
(EC2) Dd[k] = Dmens[k] ÷ Cab ÷ DM
(EC3) Diaria = Dop ÷ Cab ÷ DM
(EC4) Carop = Diaria × dias_arr
(EC5) DifAr = Varr − Carop
(EC6) Cart = Ctot ÷ Qarr
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC2 | Diária de cada item | Separar a diária por item (arrendamento, mão de obra, suplementação, outras) para ver qual pesa mais | Despesa mensal do item (R$/mês), contabilidade (arrendamento 35.000); cabeças no mês, controle de rebanho (1.000); dias por mês (30) |
| EC3 | Diária total | Custo de manter uma cabeça por um dia; exclui reposição, comissão e frete | Dop: soma das despesas operacionais do mês (R$/mês), controle de caixa (67.709,50); Cab |
| EC4 | Custo operacional da arroba | Custo de produzir 1 @ sem reposição nem juros do capital | Diaria; dias_arr de DE6 (GMD do mês: 0,583 kg/dia) |
| EC5 | Diferença entre preço e custo da arroba | Positiva, o preço cobre o custo operacional; negativa, prejuízo | Valor da @ no mês, cotação (R$ 280); Carop |
| EC6 | Custo da arroba pelo custo total | Custo por arroba incluindo todos os custos | Ctot: custo total (R$); Qarr: arrobas produzidas |

Exemplo (janeiro): 67.709,50 ÷ 1.000 cab; arrendamento 35.000 ÷ 1.000 ÷ 30 = 1,1667; mão de obra 0,1267; suplementação 0,8333; outras 0,1303; Diaria = R$ 2,2570/cab/dia; dias_arr = 30 ÷ 0,583 = 51,46; Carop = R$ 116,14/@; com @ a R$ 280, DifAr = R$ 163,86/@.

**Ponto de equilíbrio da arroba**

```
(EC7) Ddiar = dias × Diaria
(EC8) Cant = Caq + Ddiar + Dext
(EC9) PE = Cant ÷ Pf × KVA
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC7 | Despesa diária acumulada | Custo de manutenção no período de posse | Diaria (R$ 1,35); dias de permanência, datas de entrada e saída (365) |
| EC8 | Custo total do animal | Somar aquisição, diária e extras | Caq: valor de aquisição (R$ 1.900); Dext: despesa extra, contabilidade (0) |
| EC9 | Ponto de equilíbrio da arroba | Preço mínimo da @ para não ter prejuízo: PE menor que a @ atual é lucro, maior é prejuízo | Cant; Pf: peso final em kg vivo (390) |

**Exemplo**

- 1.900 + 365 × 1,35 = R$ 2.392,75; PE = 2.392,75 ÷ 390 × 30 = R$ 184,06/@; a R$ 285/@ a receita seria (390 ÷ 30) × 285 = R$ 3.705.

**Ágio na compra e preço de referência**

```
(EC10) Pkg = Varr ÷ KVA
(EC11) Pkgag = Pkg × fag
(EC12) Pu = Pkg × Pi
(EC13) Ckgc = Vcomp ÷ Pcomp
(EC14) Carc = Ckgc × KVA
(EC15) Dag = Carc − Varr
(EC16) Agt = Dag × Narrv
(EC17) Agd = Agt ÷ dias
(EC18) Gx = Agd ÷ Ckgc
(EC19) Gxg = Gx × 1.000
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC10 | Valor do kg vivo | Preço de referência por kg vivo | Valor da @ (R$ 205); kg vivo por @ (30) |
| EC11 | Kg vivo com ágio | Consulta de referência com ágio de 15%; não alimenta o orçamento | Pkg; ágio (1,15) |
| EC12 | Preço mínimo do bezerro | Referência de compra sem ágio | Pkg; Pi: peso de entrada (180 kg) |
| EC13 | Custo do kg vivo comprado | Preço pago por kg vivo | Vcomp: valor da compra, nota fiscal (R$ 1.900); Pcomp: pesagem na compra (180 kg) |
| EC14 | Custo da arroba na compra | Comparar o pago por @ com a @ do mercado | Ckgc; kg vivo por @ |
| EC15 | Ágio por arroba | Positivo, pagou acima do mercado; negativo, o restante do cálculo perde sentido | Carc; valor da @ atual, cotação do dia (R$ 290) |
| EC16 | Ágio total a diluir | Ágio em R$ por animal | Dag; Narrv de DE9, com PV igual ao peso da compra |
| EC17 | Diluição por dia | Quanto recuperar por dia de recria | Agt; dias de recria, planejamento (300) |
| EC18 | Ganho extra necessário | Ganho adicional ao normal para diluir o ágio, valorado ao preço pago por kg | Agd; Ckgc |
| EC19 | Ganho extra em gramas | Mesma leitura em g/dia | Gx |

**Exemplo**

- R$ 1.900 por 180 kg, @ a R$ 290, 300 dias: Ckgc = 10,556; Narrv = 6; Carc = R$ 316,67/@; Dag = R$ 26,67/@; Agt = R$ 160; Agd = R$ 0,5333/dia; Gx = 0,0505 kg/dia, ou 50,5 g/dia. Preço mínimo do bezerro a R$ 205/@: (205 ÷ 30) × 180 = R$ 1.230.

**Relação de troca**

```
(EC20) Vvenda = Varr × Narr
(EC21) RT = SE(Vbez > 0; Vvenda ÷ Vbez; vazio)
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC20 | Valor de venda do animal gordo | Receita de um animal gordo (repetir para machos e fêmeas, e para cada ano) | Valor da @ (R$ 255 em 2025; 320 em 2021); @ por animal (20 para macho; 14 para fêmea), peso de abate médio |
| EC21 | Relação de troca | Quantos bezerros a venda de um animal gordo compra; RT em queda indica ciclo desfavorável ao recriador e engordador | Vvenda; Vbez: valor do bezerro, notas de compra e leilões (R$ 2.100 em 2025; 3.200 em 2021) |

**Exemplo**

- machos 2025: 255 × 20 = 5.100 ÷ 2.100 = 2,43; machos 2021: 6.400 ÷ 3.200 = 2,00; fêmeas 2025: 240 × 14 = 3.360 ÷ 1.600 = 2,10; fêmeas 2021: 300 × 14 = 4.200 ÷ 2.500 = 1,68.

**Custos do ciclo do lote**

```
(EC22) Crep = Ncab × Vaq
(EC23) Arrm = farr × Varr
(EC24) Carr = Arrm × Ncab × Nmeses
(EC25) NF = Ncab ÷ CF
(EC26) Cmo = Sal × NF × Nmeses
(EC27) Cout = Outm × Ncab × Nmeses
(EC28) Cvet = Vetm × Ncab × Nmeses
(EC29) CIATF = DIATF × Ncab
(EC30) Cmc[k] = Citem[k] ÷ Ncab ÷ Nmeses
(EC31) Cmcab = Σ Cmc[k]
(EC32) Cprod = Cmcab × Nmeses
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC22 | Custo de reposição | Custo de compra do lote | Ncab (13 cab); Vaq: valor de aquisição por cabeça, nota fiscal (R$ 1.800) |
| EC23 | Arrendamento mensal por cabeça (18% da @) | Estimar o pasto como fração da @ quando não há contrato | farr (0,18); valor da @ (R$ 205) |
| EC24 | Custo de arrendamento do ciclo | Desembolso de arrendamento; usar Arrm do contrato (R$ 55/cab/mês) ou EC23 | Arrm; Ncab; Nmeses |
| EC25 | Funcionários por rateio | Ratear a mão de obra em 1 funcionário para cada 1.000 cabeças | Ncab; CF (1.000) |
| EC26 | Custo de mão de obra do ciclo | Desembolso de mão de obra (Sal já com encargos, ver EC49) | Sal: folha de pagamento, contabilidade (R$ 3.500/mês); NF: nº de funcionários (1); Nmeses |
| EC27 | Custo de outras despesas | Veterinário, combustível e demais | Outm: contabilidade (R$ 3/cab/mês) |
| EC28 | Custo de veterinária do ciclo | Sanidade no ciclo | Vetm: soma dos itens veterinários por animal por mês, notas de vacinas e medicamentos (R$ 3/cab/mês) |
| EC29 | Custo de IATF | Incluir só se o lote usa IATF | DIATF: orçamento do protocolo (R$ 80/cab, ilustrativo) |
| EC30 | Custo por cabeça por mês de cada item | Custo mensal unitário de cada item | Citem[k]: custo do item no ciclo; Ncab; Nmeses |
| EC31 | Custo mensal total por cabeça | Soma dos itens por cabeça por mês, sem reposição | Cmc[k] |
| EC32 | Custo de produção do período | Custo operacional por cabeça no ciclo (sem reposição) | Cmcab; Nmeses |

**Exemplo**

- 13 bezerras, 12,01 meses: reposição 13 × 1.800 = R$ 23.400; arrendamento 55 × 13 × 12,01 = R$ 8.584,91; outras 3 × 13 × 12,01 = R$ 468,27.

**Manutenção e economia de escopo**

```
(EC33) Cman = pman × Caqu
(EC34) EE = (Σ CY[i] − CYc) ÷ CYc
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC33 | Custo anual de manutenção | Estimar manutenção de instalações (8,0% a.a.) e máquinas (6,5% a.a.) | Caqu: custo de aquisição (R$); pman |
| EC34 | Economia de escopo | Verificar se a produção conjunta (ILP, ILPF) é mais barata que a separada; EE > 0 indica economia | CY[i]: custo de produzir só o produto i; CYc: custo conjunto |

**Mão de obra: encargos e provisões (por funcionário, mensal)**

```
(EC35) FGTS = Sal × aF
(EC36) INSSo = Sal × aI
(EC37) Enc = FGTS + INSSo
(EC38) Fer = Sal × pFer
(EC39) Ter = Sal × pTer
(EC40) D13 = Sal × p13
(EC41) Dir = Fer + Ter + D13
(EC42) FGTSd = Dir × aF
(EC43) MultM = FGTS × mF
(EC44) MultD = FGTSd × mF
(EC45) INSSd = Dir × aI
(EC46) Prov = Fer + Ter + D13 + FGTSd + MultM + MultD + INSSd
(EC47) pEnc = Enc ÷ Sal
(EC48) pProv = Prov ÷ Sal
(EC49) Cfunc = Sal + Enc + Prov
(EC50) Fat = 1 + pEnc + pProv
(EC51) Cgrp = Cfunc × NF
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| EC35 | FGTS mensal | Encargo mensal | Sal: salário, folha ou contrato (R$ 5.000); aF: 8% (0,08) |
| EC36 | INSS de outras entidades | Encargo mensal (não optante pela folha) | aI: 2,7% (0,027), contabilidade |
| EC37 | Total de encargos | Subtotal de encargos | FGTS; INSSo |
| EC38 | Férias proporcionais | Provisão mensal | pFer: 8,33% (0,0833) |
| EC39 | Um terço de férias | Provisão mensal | pTer: 2,78% (0,0278) |
| EC40 | Décimo terceiro | Provisão mensal | p13: 8,33% (0,0833) |
| EC41 | Direitos | Base de FGTS e INSS sobre direitos | Fer; Ter; D13 |
| EC42 | FGTS sobre direitos | Provisão | Dir; aF |
| EC43 | Multa do FGTS sobre a remuneração | Provisão | FGTS; mF: 40% (0,4), legislação |
| EC44 | Multa do FGTS sobre direitos | Provisão | FGTSd; mF |
| EC45 | INSS sobre direitos | Provisão | Dir; aI |
| EC46 | Total de provisões | Subtotal de provisões (não inclui os encargos) | Itens EC38 a EC45 |
| EC47 | Encargos sobre o salário | Percentual a lançar na mão de obra dos custos do lote | Enc; Sal |
| EC48 | Provisões sobre o salário | Idem | Prov; Sal |
| EC49 | Custo mensal por funcionário | Custo total do funcionário (salário + encargos + provisões) | Sal; Enc; Prov |
| EC50 | Fator de custo | Quanto o salário se multiplica no custo total | pEnc; pProv |
| EC51 | Custo mensal do grupo | Custo da equipe | Cfunc; NF: nº de funcionários, folha (1) |

**Exemplo**

- salário R$ 5.000: FGTS 400 + INSS 135 = encargos 535; férias 416,50; terço 139; 13º 416,50; FGTS sobre direitos 77,76; multa 160; multa sobre direitos 31,10; INSS sobre direitos 26,24; provisões R$ 1.267,11; Cfunc = R$ 6.802,11 (Fat = 1,3604).

**Observações**

- Duas convenções de arroba coexistem: 15 kg de carcaça com rendimento de 50,5% (cerca de 29,7 kg vivo por @), ou 30 kg de peso vivo por @ (KVA). Os custos de suplemento por @ e a diária usam 30 kg de peso vivo (ou de ganho) por @; a receita de FI1 usa a @ de carcaça.
- O custo operacional da arroba (EC4) não inclui reposição nem juros; difere do custo por arroba pelo custo total (EC6).
- Nos encargos, só entram FGTS e INSS de outras entidades; INSS patronal, RAT, vale-transporte, alimentação, EPI e horas extras ficam de fora (verificar com o contador). A multa de 40% incide só sobre o FGTS mensal, não sobre o saldo acumulado.
- A mão de obra pode ser rateada (1 funcionário por 1.000 cabeças) ou lançada com o número real de funcionários; as duas formas dão o mesmo EC26.

## Finanças e investimento (FI)

**Receita, compra, frete e comissão**

```
(FI1) Rec = Varr × Pf ÷ KC × RC × Ncab
(FI2) Rec = Ncab × Varr × (Pf ÷ KVA)
(FI3) Compra = Pu × Ncab
(FI4) Frete = Rkm × km
(FI5) Frete = Qfr × Vfr
(FI6) Com = Compra × tcom
(FI7) Cadq = Compra + Frete + Com
(FI8) Cadqcab = Cadq ÷ Ncab
(FI9) Vmov = Ncab × Vcab
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| FI1 | Receita pela arroba de carcaça | Receita bruta esperada do lote; ajustar RC se o frigorífico pagar outro rendimento | Valor da @, cotação ou frigorífico (R$ 300/@); Pf (390 kg); Ncab (13 cab); KC (15 kg); RC (0,505) |
| FI2 | Receita pela arroba de peso vivo | Receita esperada com a convenção de 30 kg vivo por @ | Valor da @ (R$ 205); Pf (390 kg); Ncab (50 cab); KVA (30) |
| FI3 | Valor total das bezerras | Custo de compra do lote | Pu: preço unitário (EC12) (R$ 1.230); Ncab |
| FI4 | Frete por distância | Conferir contra a proposta da transportadora | Rkm: frete por km do veículo (R$ 5,50); km: rota (100); uma viagem, independe da lotação do caminhão |
| FI5 | Frete por quantidade e valor unitário | Estimar o frete de cada compra registrada | Qfr (5 un.); Vfr: valor unitário, transportadora (R$ 180) |
| FI6 | Comissão de compra | Negociar corretagem ou taxa de leilão | tcom: corretor (0,015), leilão (0,03); compra |
| FI7 | Custo de aquisição | Custo de entrada do rebanho | Compra; Frete; Com |
| FI8 | Custo de aquisição por cabeça | Custo de entrada por cabeça | Cadq; Ncab |
| FI9 | Valor da movimentação | Valor financeiro de compra, venda ou transferência de um evento | Ncab: cabeças do evento; Vcab: valor por cabeça, nota fiscal (R$ 1.400 na compra; 2.800 na venda); saída com sinal negativo |

**Exemplo**

- 13 bezerras: 300 × 390 ÷ 15 × 0,505 × 13 = R$ 51.207. 50 bezerras, @ R$ 205: Rec = 50 × 205 × (390 ÷ 30) = R$ 133.250 (R$ 2.665/cab); compra 1.230 × 50 = R$ 61.500; frete 5,5 × 100 = R$ 550; comissão 1,5% = R$ 922,50; Cadq = R$ 62.972,50 (R$ 1.259,45/cab). Evento de compra: 100 cab × R$ 1.400 = R$ 140.000; frete 5 × 180 = R$ 900; comissão 3% = R$ 4.200; aquisição R$ 145.100.

**Resultado do lote**

```
(FI10) Dcic = Carr + Cmo + Csup + Cvet + CIATF + Cout
(FI11) Ctot = Cadq + Dcic
(FI12) Resultado = Rec − Ctot
(FI13) Margem = Resultado ÷ Rec × 100
(FI14) Rescab = Resultado ÷ Ncab
(FI15) Pcr = Itm ÷ Rec
(FI16) Reccab = Vendas ÷ Cab
(FI17) Ccab = Citem ÷ Cab
(FI18) Dcab = Ditem ÷ Cab
(FI19) Dcabm = Dcab ÷ Mper
(FI20) Lcab = Reccab − Ccab − Dcab
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| FI10 | Despesas do ciclo | Despesas de manutenção do ciclo (itens ausentes valem zero) | Carr (EC24); Cmo (EC26); Csup (NU16); Cvet (EC28); CIATF (EC29); Cout (EC27) |
| FI11 | Custo total do ciclo | Desembolso total; na versão com reposição, Cadq é a reposição (EC22) | Cadq (FI7); Dcic |
| FI12 | Resultado do lote | Lucro (positivo) ou prejuízo | Rec (FI1 ou FI2); Ctot |
| FI13 | Margem percentual | Comparar cenários de preço da @ ou de GMD | Resultado; Rec |
| FI14 | Resultado por animal | Lucro ou prejuízo por cabeça | Resultado; Ncab |
| FI15 | Percentual sobre a receita | Peso de cada custo ou despesa (por cabeça) na receita | Itm e receita na mesma base |
| FI16 | Receita por cabeça | Comparar com outros anos ou fazendas | Vendas: total anual (R$ 6.001.585,30); Cab: cabeças no período (1.541) |
| FI17 | Custo de aquisição por cabeça no ano | Custo de entrada por item (compras, fretes, comissão) | Citem (R$ 2.872.583,51; 125.005,92; 11.156,84); Cab |
| FI18 | Despesa por cabeça no ano | Despesa por categoria (pessoal, aluguel de pasto, sal e ração, veterinária, juros e outras) | Ditem (aluguel de pasto 721.250,40; juros 147.210,89); Cab |
| FI19 | Despesa por cabeça por mês | Custo mensal unitário | Dcab; Mper: meses do período (12) |
| FI20 | Resultado por cabeça no ano | Lucro por cabeça (multiplicar por Cab para o total) | Reccab; Ccab; Dcab |

Exemplo (13 bezerras): despesas do ciclo R$ 11.388,66; custo total R$ 34.788,66; resultado R$ 16.418,34; margem 32,06%; R$ 1.262,95/animal. Exemplo anual: 1.541 cabeças, vendas R$ 6.001.585,30 = R$ 3.894,60/cab; custos R$ 1.952,46/cab (50,13%); despesas R$ 922,20/cab/ano (R$ 76,85/mês; 23,68%); resultado 3.894,60 − 1.952,46 − 922,20 = R$ 1.019,95/cab (26,19%), ou R$ 1.571.735,69 no total. Lote de 50 bezerras: despesas R$ 40.598,20, lucro R$ 29.679,30 (R$ 593,59/cab; 22,27% da receita).

**Capital, caixa e dívidas**

```
(FI21) Aporte = pAP × Ncab × Varr × (Pf ÷ KVA)
(FI22) Capital = Aporte − Ret
(FI23) Saldo = Rec + Capital − Cadq − Dcic + Ajus
(FI24) Cabex = Resultado ÷ Pu
(FI25) Repos = Cadqcab × Ncab
(FI26) Resemp = Receb − Pagos
(FI27) Apagar = Ncab × Pcab
(FI28) Caixa = Σ Sbanco[j]
(FI29) Divida = Σ Venc[a]
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| FI21 | Aporte de capital sugerido | Capital mínimo para o lote: 80% da receita bruta de venda | Varr (R$ 205/@); Ncab (50); Pf (390 kg); pAP (0,8) |
| FI22 | Capital inicial | Capital de giro disponível | Aporte; Ret: retiradas, contabilidade (0) |
| FI23 | Saldo em caixa | Caixa projetado ao fim do ciclo | Rec; Capital; Cadq; Dcic; Ajus: ajuste, contabilidade (0) |
| FI24 | Cabeças a mais com o lucro | Quantas bezerras o lucro compraria | Resultado; Pu |
| FI25 | Valor de reposição | Reposição do rebanho ao custo de entrada | Cadqcab; Ncab |
| FI26 | Saldo de empréstimos | Sobra ou falta de caixa com empréstimos | Receb: recebidos (R$ 3.236.351,99); Pagos (R$ 3.406.908,25), extratos e contratos |
| FI27 | Contas a pagar de gado | Dívidas futuras com compra de animais | Ncab (54; 96); Pcab: preço por cabeça (R$ 1.650) |
| FI28 | Caixas e bancos | Saldo disponível | Sbanco[j]: saldos em extratos (305.250; 40.893,89; 4.781,65; 23.918; 2.006 R$) |
| FI29 | Total de vencimentos | Cronograma de dívidas | Venc[a]: contratos de financiamento por ano |

**Exemplo**

- aporte 205 × 50 × (390 ÷ 30) × 0,8 = R$ 106.600; saldo em caixa R$ 136.279,30; empréstimos 3.236.351,99 − 3.406.908,25 = −R$ 170.556,26.

**Fluxo de caixa mensal e anual**

```
(FI30) Rtot = Σ Rcat[k]
(FI31) Dtot = Σ Dcat[k]
(FI32) Itot = Σ Icat[k]
(FI33) Acum = Resano(ano−1)
(FI34) Resano = Rtot − Dtot + Acum
(FI35) Resmes = Rtotm − Dtotm
(FI36) Rgeral = Rtot − Dtot − Itot
(FI37) Acg = Rgeral(ano−1) + Acg(ano−1)
(FI38) Sopc = Est + Acg + Rgeral
(FI39) Sops = Est + Acg + Rgeral + Itot + Iant
(FI40) Cger = Cmp + Csup + Carr + Cvet + Cmo + Cdem
(FI41) Pvm = Rtot ÷ Cvend
(FI42) Mit[k] = Citem[k] ÷ Cadqd
(FI43) Mdesp = Σ Mit[k]
(FI44) Rescab2 = Pvm − Mdesp
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| FI30 | Total de receitas | Receita do mês ou do ano (5 categorias: fêmeas e machos para abate, reprodução, desmama, outras) | Rcat[k]: notas de venda, frigorífico, extrato (R$ 300.000 em jan) |
| FI31 | Total de despesas | Despesa do mês ou do ano (15 categorias: reposição, comissões, fretes, arrendamento, mão de obra, suplementação, IATF, veterinária, insumos, combustíveis, veículos, material, juros, taxas e impostos, outras) | Dcat[k]: notas, folha, contrato (R$ 20.000; 250; 560; 3.500) |
| FI32 | Total de investimentos | Investimento em cercas e curral, reforma de pastos, construções, máquinas, veículos e utensílios | Icat[k]: notas de obras e equipamentos (R$ 2.500; 3.600; 360; 290; 3.200) |
| FI33 | Acumulado do ano anterior | Saldo trazido do ano anterior | Resultado financeiro do ano anterior |
| FI34 | Resultado financeiro do ano | Positivo, sobra de caixa operacional; negativo, o ano consumiu caixa; não considera investimentos | Rtot; Dtot; Acum |
| FI35 | Resultado financeiro do mês | Ver quando o caixa aperta | Receita e despesa do mês |
| FI36 | Resultado geral da atividade | Resultado depois de investir em cercas, pastos e construções | Rtot; Dtot; Itot |
| FI37 | Resultado geral acumulado | Saldo trazido do ano anterior | Rgeral e Acg do ano anterior |
| FI38 | Saldo da operação com investimentos | Visão patrimonial | Est: valor do estoque de rebanho (GE8); Acg; Rgeral |
| FI39 | Saldo da operação sem investimentos | Devolver os investimentos para ver o desempenho operacional puro | Itot do ano; Iant: investimentos dos anos anteriores |
| FI40 | Custo geral multi-ano | Fechar o custo total com o total de despesas | Cmp: compras (reposição + comissão + frete); Csup; Carr; Cvet; Cmo; Cdem: demais custos (IATF, insumos, combustíveis, veículos, material, juros, taxas e outras) |
| FI41 | Preço médio de venda | Receita por cabeça vendida (zero sem venda) | Rtot; Cvend: cabeças vendidas |
| FI42 | Custo médio por item por cabeça adquirida | Custo médio de cada item (zero sem aquisição) | Citem[k]; Cadqd: cabeças adquiridas |
| FI43 | Média de despesas por cabeça | Despesa média por cabeça | Mit[k] |
| FI44 | Resultado por cabeça adquirida | Resultado médio por cabeça no consolidado | Pvm; Mdesp |

**Exemplo**

- ano seguinte: receita 300.000 − despesas 0 + acumulado de 33.163 (despesas do ano anterior) = 266.837; venda média 400.000 ÷ 130 = R$ 3.076,92/cab; resultado por cabeça 3.076,92 − 68,71 = R$ 3.008,21.

**Rentabilidade do projeto**

```
(FI45) VPL = Σ (Rt − Ct) ÷ (1 + i)^t
(FI46) 0 = Σ (Rt − Ct) ÷ (1 + TIR)^t
(FI47) Lop = Rbruta − COT
(FI48) Lecon = Lop − Copor
(FI49) Rmod = RLha × Amod
(FI50) Mha = Parrha × Marr
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| FI45 | Valor presente líquido | Valor presente do fluxo de 10 anos (t de 0 a 10) | Rt e Ct por ano (R$); i: taxa de desconto (10% a.a.) |
| FI46 | Taxa interna de retorno | Rentabilidade pela taxa que zera o VPL | Fluxo de caixa anual |
| FI47 | Lucro operacional | Resultado operacional do sistema (R$/ha.ano); também a receita líquida por hectare | Rbruta e COT, custo operacional total (R$ 8.321 e 4.341 R$/ha.ano) |
| FI48 | Lucro econômico | Descontar o custo de oportunidade da terra e do capital | Lop; Copor: arrendamento (R$ 15/cab/mês) mais poupança (0,5% a.m. + TR) |
| FI49 | Receita líquida do módulo | Converter a receita por hectare na receita do módulo | RLha (R$ 3.980/ha.ano); Amod (5,2 ha) |
| FI50 | Margem por hectare | Margem por hectare a partir da produtividade | Parrha (5,57 @/ha); Marr: margem por @ (US$ 17 bruta; 10 líquida) |

**Exemplo**

- 8.321 − 4.341 = R$ 3.980/ha.ano; 3.980 × 5,2 = R$ 20.696 (cerca de R$ 20.700/ano ou R$ 1.725/mês); margem por hectare 5,57 × 17 = US$ 94,7 (bruta) e 5,57 × 10 = US$ 55,7 (líquida).

**Observações**

- As fórmulas de receita diferem na convenção de arroba: FI1 usa a @ de carcaça (15 kg × 50,5%, cerca de 29,7 kg vivo por @) e FI2 usa 30 kg de peso vivo por @ (equivale a 50% de rendimento); as diferenças de resultado são pequenas.
- A despesa por cabeça por mês usa 12 meses fixos (Mper), mesmo quando o prazo calculado do ciclo é de 12,01 meses.
- Valorizar o estoque pelo custo médio de compra por cabeça (R$ 43,35) fica muito abaixo do preço de venda (R$ 3.077/cab); informe um valor de avaliação por cabeça se quiser uma visão patrimonial realista.
- O "acumulado" FI33 é o resultado do ano anterior somado; confirmar a convenção de sinal na contabilidade da fazenda.
- Não há linha própria de total do caixa; empréstimos pagos devem ser separados do saldo líquido para evitar ambiguidade.
- Frete e comissão de compra são lançados uma vez por evento de compra, não por cabeça; a comissão do corretor (1,5%) e a de leilão (3%) diferem.

## Gestão, estoque e indicadores (GE)

**Utilização e eficiência da terra**

```
(GE1) Aap = Aimov − (RL + APP + Aeco + Aref + Aben)
(GE2) GUT = Aut ÷ Aap × 100
(GE3) Aeqp = UAmed ÷ ILZP
(GE4) Aeqa = Qcol ÷ IPr
(GE5) GEE = (Aeqp + Aeqa) ÷ Aap × 100
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| GE1 | Área aproveitável | Base de cálculo do GUT e do GEE | Aimov, RL, APP, Aeco: interesse ecológico, Aref: reforma comprovada, Aben: benfeitorias (ha), cadastro do imóvel |
| GE2 | Grau de utilização da terra | Aproveitamento da terra; meta ≥ 80% | Aut: área utilizada (ha); Aap |
| GE3 | Área equivalente da pecuária | Converter a pecuária em área equivalente | UAmed: UA total média de 12 meses, soma das UAcat de PA3; ILZP: índice de lotação da zona de pastagem (UA/ha) |
| GE4 | Área equivalente da agricultura | Converter a lavoura em área equivalente | Qcol: quantidade colhida; IPr: índice de produtividade regional |
| GE5 | Grau de eficiência da terra | Eficiência da terra; meta ≥ 100% | Aeqp; Aeqa; Aap |

**Rebanho, estoque e carga de trabalho**

```
(GE6) Sfim = Sini + Comp − Vend + Transf
(GE7) Sper = Sini1 + Σ Comp − Σ Vend + Σ Transf
(GE8) Est = Cab × Cmcomp
(GE9) Cabfun = Cab ÷ NF
(GE10) Hafun = Aman ÷ NF
(GE11) Ocup = Nemb ÷ Ccam × 100
(GE12) Perdcam = 100 − Ocup
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| GE6 | Saldo final de cabeças do evento | Conferir cada evento com a contagem física, em ordem cronológica | Sini: saldo inicial, inventário (0); Comp e Vend: GTA e nota fiscal (100; 25); Transf: transferências, nascimentos e mortes com sinal, caderneta de campo (+3; −1; −5; +8) |
| GE7 | Saldo do período | Resumo do período a partir do saldo inicial da primeira linha | Eventos do período |
| GE8 | Valor do estoque de rebanho | Valorizar o rebanho restante | Cab: rebanho de referência; Cmcomp: custo médio de compra por cabeça ou valor de avaliação (R$ 43,35/cab) |
| GE9 | Cabeças por funcionário | Carga de trabalho | Cab; NF |
| GE10 | Hectares por funcionário | Área manejada por funcionário (média 434; pasto 257) | Aman: área total ou de pasto (ha); NF |
| GE11 | Ocupação do caminhão | Calcular a ocupação do caminhão | Nemb: animais embarcados (22); Ccam: capacidade (26) |
| GE12 | Capacidade não usada do caminhão | Medir a ociosidade do caminhão | Ocup |

**Exemplo**

- 100 − 25 + (3 − 1 − 5 + 8) = 80 cabeças; ocupação 22 ÷ 26 = 84,6% (perda de capacidade 15,4%).

**Totais, médias, questionários e variações**

```
(GE13) Tano = Σ Vmes[m]
(GE14) Vmed = Σ Vmes[m] ÷ nm
(GE15) Mj = Σ qdim[k] ÷ nq
(GE16) Nj = Mj ÷ 5 × 100
(GE17) GMGC = Σ wj × Nj ÷ 100
(GE18) Tresp = Nresp ÷ Nenv × 100
(GE19) Var = (Vf − Vi) ÷ Vi × 100
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| GE13 | Total anual | Total do ano de cada linha de despesa, receita ou cabeças | Vmes[m]: valor de cada um dos 12 meses |
| GE14 | Média mensal | Média do ano ignorando meses sem dado | Vmes[m]; nm: nº de meses com valor diferente de zero |
| GE15 | Pontuação por dimensão | Nota da dimensão do questionário (escala 1 a 5) | qdim[k]: respostas do questionário |
| GE16 | Nota normalizada | Levar a nota da dimensão à escala 0 a 100 | Mj (média 2,76 dá 55,2) |
| GE17 | Grau de maturidade em gestão do conhecimento | Maturidade de 0 a 100 | wj: pesos das 7 dimensões (soma 100); Nj |
| GE18 | Taxa de resposta | Taxa de resposta do questionário | Nresp (94); Nenv: efetivamente enviados (318) |
| GE19 | Variação percentual | Crescimento de produção, consumo ou diferença entre cenários | Vi e Vf: valores inicial e final (9.206 → 11.355 mil TEC; 7.188 → 8.471) |

**Exemplo**

- variação de produção (11.355 − 9.206) ÷ 9.206 = 23,3%; consumo interno 17,8%; cenários de pasto degradado (31,93 − 12,49) ÷ 12,49 = 155,6%; taxa de resposta 94 ÷ 318 = 29,6% (29,2% reportado).

**Observações**

- O rebanho do período (Cab) segue o saldo de GE6: saldo do fim do ano anterior, mais aquisições, menos vendas, mês a mês. A diária anual é a média das diárias mensais maiores que zero (GE14).
- O GUT tem meta mínima de 80% e o GEE de 100%.
- Os pesos das 7 dimensões do GMGC (LI, PRO, PES, TEC, PC, AI, RGCS) não são conhecidos; a reconstrução por média simples (Mj ÷ 5 × 100) dá 55,2 contra 55,9 reportado.
- Na contagem de cabeças, o saldo inicial seguinte fica em branco se uma linha ficar totalmente vazia (compras, vendas e transferências).
- A perda de capacidade do caminhão calculada (15,4%) difere do valor declarado em referência (20%).

## Sustentabilidade e território (SU)

**Razões de área e produtividade, redução de emissões**

```
(SU1) Rarea = Aext ÷ Asist
(SU2) Redarea = (1 − Asist ÷ Aext) × 100
(SU3) Rprod = Pprod1 ÷ Pprod2
(SU4) Redem = (E0 − E1) ÷ E0 × 100
(SU5) Part = Vcarne ÷ Vagro × 100
```

| Eq. | Nome | Quando usar | Dados necessários |
|---|---|---|---|
| SU1 | Razão de área | Comparar a área exigida por dois sistemas | Aext: área do sistema extensivo (12,1 ha); Asist: área do sistema intensivo (5,2 ha) |
| SU2 | Redução percentual de área | Quanto menos terra o sistema intensivo precisa | Asist; Aext |
| SU3 | Razão de produtividade por área | Ilustrar o baixo rendimento por área da pecuária (produtos diferentes) | Pprod1 e Pprod2: kg/ha de cada produto (soja 2.800; cana 75.000; pecuária 50) |
| SU4 | Redução percentual de emissão | Ganho ambiental de abater mais cedo | E0: emissão antes (112,5 kg CH4/animal, abate aos 39 meses); E1: depois (78,7, abate aos 29 meses) |
| SU5 | Participação no valor do agronegócio | Peso da carne no PIB do agro | Vcarne e Vagro em US$ bi (116 e 374) |

**Exemplo**

- 12,1 ÷ 5,2 = 2,33 (valor reportado: 2,4); redução de área 1 − 5,2 ÷ 12,1 = 57% (valor reportado: 54%); soja ÷ pecuária 2.800 ÷ 50 = 56; cana ÷ pecuária 75.000 ÷ 50 = 1.500; metano (112,5 − 78,7) ÷ 112,5 = 30%; carne no agro 116 ÷ 374 ≈ 31%.

**Observações**

- As razões de produtividade comparam produtos diferentes e servem só como ilustração de ordem de grandeza.
- A participação da carne é valor reportado, não um cálculo.
