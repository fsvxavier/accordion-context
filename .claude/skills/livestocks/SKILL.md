---
name: livestocks
description: "Use when working on pecuária bovina (corte e leite) no Brasil: manejo de pastagem, adubação, cria/recria/engorda, confinamento, suplementação, reprodução e genética, sanidade, qualidade do leite (CCS/CBT), conforto/bem-estar, instalações, boas práticas (BPA), intensificação, ILP/ILPF, silvipastoril, Pantanal/Amazônia, rastreabilidade, indicadores zootécnicos e fórmulas (GPD, CMS, lotação, heterose). Base: 14 documentos técnicos de pecuária."
---

<!-- argument-hint: [tema, fórmula ou ID de equação, ex.: confinamento, CMS, NU1] -->

# Livestocks — pecuária bovina (corte, leite, sustentabilidade)
**Base**: 14 documentos técnicos (~1.000 págs) | **Referências**: 14 temas + fórmulas, cheatsheet, glossário e padrões | **Gerado**: 2026-10-26

## Como usar
- Sem argumento: use o Núcleo abaixo.
- Com tema: abra o arquivo temático em [references/](references/README.md) (índice abaixo) antes de responder; cite número e condição.
- Fórmula: [formulas.md](references/formulas.md), 255 equações em 11 áreas (DE desempenho, RE reprodução, NU nutrição, PA pastagem, LE leite, SA sanidade, IN instalações, EC custos, FI finanças, GE gestão, SU sustentabilidade) com legenda global de símbolos; arquivo grande: use grep no ID (ex.: NU1) ou leia só a área.
- Decisão e limiares: [cheatsheet.md](references/cheatsheet.md). Termo: [glossary.md](references/glossary.md). Método: [patterns.md](references/patterns.md).
- Antes de responder, leia a seção inteira do arquivo temático (não só o Núcleo) e traga todos os números e condições da seção; para cálculo, escreva a fórmula, os dados e a conta.
- Conflito entre valores ou erro de fonte: os arquivos sinalizam em "Inconsistências"; apresente os valores citados e não escolha em silêncio. Em pergunta sobre um valor que varia (idade de desmama, sombra por animal, peso da alimentação no custo, tamanho de lote), leia a seção "Inconsistências" do arquivo antes de afirmar que não há divergência.

## Núcleo (valores citados das fontes; confira no arquivo do tema antes de usar em decisão)
- **Intensificação** (01): parâmetros mínimos de cria: prenhez ≥75%, desmame >160 kg, repetição de cria ≥65%. Extensivo, semi-intensivo e intensivo se distinguem por terra x mão de obra x insumos.
- **Estação de monta e ECC** (02): condição corporal na parição e na monta comanda a prenhez; monta ~3 meses; andrológico antes da monta; diagnóstico de gestação ~45 d; novilha a ~65% do peso adulto.
- **Boi sanfona** (04, 05): ganho na água, perda na seca; corrigir com suplementação na seca e pasto diferido.
- **Confinamento** (05): adaptação 10-20 dias; piquete 8-12 m²/animal, ~100 animais/lote, cocho 0,35-0,70 m; leitura de cocho nota 1-5 (3 é o ideal) com ajuste de ~0,5% da oferta; caroço de algodão ≤15% da MS.
- **Forragem mínima** (04, 06): 2.500 kg MS/ha (B. decumbens; a fonte grafa "t", erro de escala).
- **Leite, qualidade** (08): resfriar a 4 °C em até 3 h; tanque ≈ 2,5 x produção diária (atual e futura); CCS >200 mil/mL indica mastite; caneca de fundo escuro, CMT, pré e pós-dipping.
- **Leite a pasto** (06, 08): nº de piquetes = PD/PO + 1 (PD dias de descanso, PO dias de ocupação); resposta ao N com pico em ~750 kg N/ha com irrigação.
- **Conforto térmico** (09): estresse quando retal ≥39,4 °C, FR ≥80/min, em ≥70% do lote; sombra, aspersão e nebulização; o pacote sombra + ventilação + aspersão dá o maior ganho de leite (18,20 → 19,19 → 20,53 kg/vaca/ordenha); free-stall 1 baia/vaca.
- **Genética** (02): heterose H = (X1 − X2)/X2 x 100; DEP/EPD com acurácia; rotacionado 2 raças ≈67% de heterose retida, 3 raças ≈86%.
- **Pré-abate** (09): evitar contusão e DFD (pH alto); carregar sem estresse; sem jejum na fazenda.
- **Gestão e BPA** (11): GUT ≥80% e GEE ≥100% (função social do imóvel); a fórmula do GEE tem duas redações na fonte.
- **Sustentabilidade** (07, 13): ILP/ILPF, SSPR (silvipastoril, módulo 5,2 ha, 24 piquetes), rastreabilidade, GTPS/GRSB, abordagem territorial (PCI/PPI/VSA).
- **Arroba e suplemento novo** (01, 04): 1 @ = 15 kg de carcaça (algumas calculadoras usam 30 kg de peso vivo); adaptação a um suplemento novo em 2 a 3 semanas; suplementar funciona mesmo com forragem >4 t MS/ha.
- **Ingrediente em matéria natural** (05, NU3): MNing = CMS × fing ÷ MSing. **N pelo balanço** (06): 15.000 kg MS × 1,5% = 225 kg N, menos 100 kg N da matéria orgânica = 125 kg N/ha.ano, ureia ≈ 555 kg/ha (PA15).
- **Desmama** (03): lado a lado por 3 dias (pastos vizinhos); idade citada 6-8, 7-9 (exemplo trabalhado: 7 meses) ou 9-11 meses (natural); meta 160-180 kg; colostro em até 3 h.
- **Mercado e território** (12, 13): Rondônia livre de aftosa com vacinação desde 21/05/2003; Pantanal com 86% do bioma conservado; cotações de mercado não estão na skill (consulte fonte atualizada).
- **Medicamento** (10): a skill não traz dose de ivermectina para adultos; a única menção é a exceção de 1 ml de Ivomec em bezerro no protocolo orgânico do Pantanal; dose é com o veterinário.
- **Gestão do conhecimento** (14): PIC e MMGC (7 dimensões, 4 níveis); a fórmula do índice é reconstrução.

## Índice de referências
| Arquivo | Escopo |
|---|---|
| [01 sistemas e intensificação](references/01-sistemas-producao-intensificacao.md) | extensivo, semi-intensivo, intensivo; parâmetros mínimos; idade de abate; índices |
| [02 reprodução e genética](references/02-reproducao-genetica.md) | estação de monta, ECC, IA, DEP, cruzamentos, heterose |
| [03 cria, bezerros e recria](references/03-cria-bezerros-recria.md) | parto, colostro, desmama, creep-feeding, recria |
| [04 nutrição e suplementação](references/04-nutricao-suplementacao.md) | suplementação seca e águas, ingredientes, princípio ruminal, feno e silagem, água |
| [05 confinamento e terminação](references/05-confinamento-terminacao.md) | adaptação, dimensionamento, leitura de cocho, pontos críticos, custos |
| [06 pastagem e adubação](references/06-pastagem-adubacao-forragem.md) | calagem, adubação, recuperação e reforma, piquetes, irrigação, sobressemeadura, alfafa |
| [07 integrados e silvipastoril](references/07-sistemas-integrados-silvipastoril.md) | ILP, ILPF, SSPR, resultados financeiros, crédito, carbono |
| [08 leite e ordenha](references/08-leite-qualidade-ordenha.md) | CCS, CBT, ordenha, higienização, resfriamento, coleta, mastite |
| [09 conforto e instalações](references/09-conforto-bem-estar-instalacoes-pre-abate.md) | estresse térmico, free-stall, instalações, transporte, contusões |
| [10 sanidade](references/10-sanidade.md) | vacinas, doenças, parasitas, cascos, plano sanitário |
| [11 gestão e finanças](references/11-gestao-economia-financas.md) | funções administrativas, GUT/GEE, custos, encargos |
| [12 legislação e rastreabilidade](references/12-legislacao-ambiental-rastreabilidade.md) | APP, reserva legal, licenciamento, identificação, GTA, normas |
| [13 sustentabilidade e mercado](references/13-sustentabilidade-mercado-certificacao-territorio.md) | carne orgânica, GTPS/GRSB, abordagem territorial, desmatamento evitado, metano |
| [14 gestão do conhecimento](references/14-gestao-conhecimento-inovacao.md) | maturidade em gestão do conhecimento, diagnóstico, métodos |

## Índice de tópicos
- Adubação, calagem, saturação por bases → 06
- Alizarol, acidez, crioscopia → 08
- Alfafa, sobressemeadura, irrigação → 06
- Bem-estar, transporte, contusão → 09
- Bezerro, colostro, desmama, creep-feeding → 03
- Carrapato, verminose, vacinas, aftosa, brucelose → 10
- CCS, CBT, mastite → 08, 10
- CMS, dieta de confinamento, leitura de cocho → 05, 04
- Crédito rural, Plano ABC, REDD+ → 07
- Cruzamentos, DEP, heterose → 02
- Custos, VPL, TIR → 07, 11, 05
- Desmatamento, reserva legal, APP → 12, 13
- Free-stall, estresse térmico, sombra → 09
- Gestão, indicadores zootécnicos (GPD, @/ha, lotação) → 11, 01
- ILP, ILPF, silvipastoril → 07
- Intensificação → 01
- Lotação, UA, capacidade de suporte → 01, 06
- Ordenha, higienização, 5S, água → 08
- Pantanal, carne orgânica, certificação → 13
- Pastejo rotacionado, piquetes → 06
- Rastreabilidade, GTA, identificação → 12
- Reprodução, estação de monta, IA → 02
- Suplementação seca e águas → 04
- Terminação a pasto e confinamento → 05, 04
- Gestão do conhecimento, MMGC → 14

## Limites
- Planilhas Excel: originais em PJT/pecuaria/planilhas/; cópias corrigidas em PJT/pecuaria/planilhas/corrigidas/ (valores só aparecem ao abrir no Excel; preços e valores em células amarelas são exemplos a atualizar). As fórmulas das planilhas estão em formulas.md.
- Só cobre o conteúdo dos 14 documentos; legislação (IN 7/2016, Código Florestal 2012) pode ter mudado; revalide.
- Alguns documentos são slides com extração parcial, um perdeu a tabela de pesos e a fórmula do índice, outro tem tradução automática ruim; os arquivos marcam o que é derivado ou duvidoso.
- Não substitui veterinário ou agrônomo para dose, diagnóstico ou receituário.
