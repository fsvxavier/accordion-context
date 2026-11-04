# Rastreabilidade e conformidade regulatória (PNIB, SISBOV, GTA, LGPD)

Regra geral: cada regra regulatória registra **fonte, jurisdição, vigência, última revisão** e comportamento quando a fonte/serviço está indisponível. Integração oficial nunca é descrita como emissão ou garantia de conformidade sem autorização, documentação, testes e validação por responsável legal. Pesquisa de 01/08/2026; cronogramas podem mudar por nova portaria — revisar a cada marco.

## PNIB (Plano Nacional de Identificação Individual de Bovinos e Búfalos)
Base: Portaria SDA/MAPA nº 1.331, de 21/07/2025. **Não é "obrigatório até 2032"**; o cronograma é incremental:
| Etapa | Período | Exigência |
|---|---|---|
| 1 | desde 01/07/2025 | Sistema federal e Base Central de Dados (infra do MAPA) |
| 2 | até 31/12/2026 | OESAs estaduais adequam sistemas ao padrão técnico do MAPA |
| 3 | jan/2027 a dez/2029 | Identificação individual **obrigatória só** para animais em manejos sanitários específicos (ex.: vacinação contra brucelose) ou em protocolos privados homologados pelo MAPA; movimentar não identificado ainda é permitido fora desses casos |
| 4 | 2030 a 2032 | Ampliação progressiva (transitória) |
| Vigência plena | **a partir de 01/01/2033** | Proibido transportar/movimentar bovinos e búfalos não identificados e não cadastrados |
Estados podem antecipar se concluíram etapas 1 e 2; legislação estadual complementar é permitida desde que não conflite com a federal.

**Implicações de produto:** (a) suportar **identificação parcial** como estado normal (≥2029); ausência de brinco não é erro; (b) modelar vacinação de brucelose como **gatilho de identificação obrigatória** (valor imediato na etapa 3); (c) parametrizar por UF e vigência; (d) o nome do plano inclui búfalos, mas isso não decide o escopo bubalino do produto.

## SISBOV
**Adesão voluntária**, exceto quando ato normativo ou programa específico exige (exportação, protocolo sanitário, certificação de rastreabilidade). GTA integra-se ao SISBOV para animais de exportação ou certificados. Cadastro básico de animal funciona sem SISBOV; emissão de anexos acionada por tenant que participa de protocolo que exija. Certificação incremental por lote.

## GTA (Guia de Trânsito Animal)
Emitida pelo **órgão estadual de defesa agropecuária** de cada UF (IMA-MG, CIDASC-SC, IAGRO-MS, INDEA-MT, ADAPAR-PR, CDA-SP, AGRODEFESA-GO…). **Não há API nacional única**; cada estado tem sistema, credenciamento e autenticação próprios. Isso explica por que a integração de GTA é rara e custosa: é uma barreira multiplicada por 27 UFs.
Arquitetura (padrão de conectores híbrido, ports/adapters):
1. **Conector declarativo (YAML)** para UF com REST/webhook simples e token — nova UF por configuração, sem deploy.
2. **Conector nativo (Go)** para UF com SOAP legado, fluxo stateful ou regra própria.
3. **Fallback obrigatório:** emissão manual assistida (checklist de campos + PDF pré-preenchido) quando não há conector; nunca bloqueia venda/transporte.
Estado atual: **nenhuma UF tem conector oficial homologado**; a cobertura real é zero até F3, então qualquer texto comercial só pode prometer "GTA pré-preenchida/assistida" e nunca "emissão automática" nem "em todo o Brasil". Lista de UFs prontas deve vir de telemetria e homologação, não de suposição.
Sequência: começar pelas UFs de maior rebanho (MT, MS, GO, PA, MG), expandir por demanda real (telemetria de UF mais solicitada). Integração assíncrona, idempotente, guardando número e PDF.

## LGPD (Lei 13.709/2018)
**Não há prazo único de retenção.** Art. 15: término do tratamento quando se esgota a finalidade; art. 16: conservação só para obrigação legal/regulatória, estudo com anonimização, transferência legal, uso exclusivo do controlador anonimizado. Retenção **por categoria de dado**, em tabela de política (categoria, base legal, prazo, gatilho, ação anonimizar/excluir):
| Categoria | Base | Referência |
|---|---|---|
| Fiscal/tributário (NF-e, LCDPR) | CTN art. 173 | 5 anos do fato gerador |
| Trabalhista (se tratado) | CLT art. 11, FGTS | 5 anos pós-rescisão; FGTS até 30 anos (responsabilidade do empregador) |
| Logs de auditoria sem obrigação específica | Código Civil arts. 205/206 | 3 a 10 anos (teto) |
| Rastreabilidade sanitária (SISBOV/GTA/PNIB) | norma do órgão de defesa | conforme norma vigente, em geral maior que prazos civis |
Mecanismo de exclusão: soft delete → scrub de PII → expurgo de auditoria, com prazo do 3º passo parametrizável. Exportação, correção, exclusão e portabilidade têm fluxo por finalidade/base legal e revisão jurídica. DPO nomeado, subprocessadores listados, procedimento de incidente e DPA para cliente PJ são práticas de governança que aumentam a confiança de clientes PJ.

## Fiscal do produtor
NF-e, MDF-e, LCDPR: ver financeiro. MDF-e obrigatório conforme valor/percurso (frequentemente interestadual) usando a mesma infraestrutura de certificado e webservice.

## Matriz de prioridade (reconciliada com o roadmap)
Integração **oficial** (SISBOV, GTA, fiscal) só em F3, após checar requisitos vigentes, autorização e homologação por UF. Por fase:
- F1: PNIB identificação parcial (múltiplos identificadores); LGPD por categoria (F0/F1).
- F2: gatilho de brucelose como evento configurável; GTA e SISBOV em modo **assistido** (checklist + PDF pré-preenchido, sem emissão oficial); pesquisa e piloto do primeiro conector de UF; fiscal apenas como especificação e addon planejado.
- F3: conectores oficiais de GTA por UF (maior rebanho primeiro), SISBOV oficial, NF-e/MDF-e/LCDPR homologados.
Nenhum desses entra no MVP (F1).
