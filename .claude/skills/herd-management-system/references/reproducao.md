# Reprodução

## Entidades
- **Estação de monta/reprodutiva**: datas, lotes/fêmeas elegíveis, touro(s), método, responsáveis. Relatórios reprodutivos exigem estação cadastrada (alerta bloqueante se faltar).
- **Eventos**: cio/observação, cobertura natural (monta natural/induzida), IA, **IATF**, FIV, TE (P2), implante/retirada de progesterona, diagnóstico de gestação (DG), parto, aborto/perda, desmame, descarte reprodutivo. Cada evento: data, executor, protocolo, sêmen/reprodutor, resultado.
- Status reprodutivo da fêmea (vazia, inseminada, prenhe, parida, descarte; "dúvida em toque", "rufião" como estados especiais). Fêmea fora de "vivo" não entra em seleção.
- Validação de completude: evento reprodutivo só salva com dados relevantes preenchidos.

## Gestação e previsão de parto
- **Parâmetro de gestação configurável por propriedade, raça/espécie, com versão e fonte**; padrão para bovinos de corte em torno de 283 dias (zebuínos costumam ficar acima, ~290; validar com técnico). Previsão = data de cobertura/IA/IATF efetiva + parâmetro. Usar default quando não preenchido, mas exibir qual parâmetro foi usado.
- DG por palpação ou ultrassom, com data, método, profissional e referência ao evento; DG negativo aparece nos relatórios (não filtrar fracassos). Janela de seleção de exposição até 9 meses.
- Parto: cria, sexo, data, peso opcional, mãe; cria nasce como animal com vínculo mãe/cria.

## Paternidade e touro múltiplo
Quando vários touros cobrem o mesmo lote, ou sêmen/touro incerto, paternidade é **conjunto de candidatos** com grau (conhecida / provável / desconhecida) até confirmação (DNA). Nunca inventar pai definitivo. Genealogia guarda fonte e confiabilidade.

## Protocolos hormonais (IATF)
Protocolo como **entidade cadastrável com versão** e biblioteca de modelos editáveis, composta por passos com dia relativo (D0, D8, D10…), produto, dose e responsável. Lote de IATF com datas de procedimento e **alertas por celular/e-mail**. Exemplo de estrutura usual (apenas ilustração, validar com veterinário): implante + benzoato no D0; retirada + prostaglandina e outros no D8; IA em tempo fixo ~D10; DG ~30 dias depois. Defaults identificados como editáveis; nunca prescrição.
Comparar modalidades (IATF × monta natural × IA convencional) e avaliar touro (andrológico, desempenho).

## Estoque biológico
Sêmen e embriões por botijão/caneca/dose/touro/localização: entradas, consumo, validade. Cada aplicação reconcilia e **baixa a dose uma vez** (idempotência). Eficácia por sêmen/touro: inseminações que geram prenhez. Reprodutores externos permitidos. Integra com financeiro (compra).

## Indicadores (declarar população elegível e denominador)
Taxa de prenhez, natalidade, concepção, **doses por prenhez**, % aproveitamento de fêmeas, vacas expostas × aptas, taxa de desmame, kg desmamado por vaca exposta, **intervalo entre partos (IEP)** e data do último parto, idade ao primeiro parto, taxa de desfrute, mortalidade por causa. Referência técnica geral: IEP-alvo ≈ 12–13 meses; usar como benchmark parametrizável, não regra.

## FIV/TE e serviços especializados
FIV/TE e protocolos especiais só após validação com técnicos e contratos de dados de programas genéticos (P2). Cobertura completa esperada: IA, IATF, FIV, TE, monta natural e touro múltiplo. O técnico de reprodução que atende várias fazendas é canal: app próprio com agenda por cliente/fazenda/grupo e cobrança por créditos.

## Armadilhas
- Previsão de parto sem registrar qual parâmetro foi usado.
- Baixa de sêmen duplicada em retry offline.
- Esconder DG negativo ou aborto dos denominadores.
- Campos reprodutivos de fêmea publicados mas incompletos.
