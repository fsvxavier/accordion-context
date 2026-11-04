# Custos e financeiro operacional

Painel é **gerencial**, nunca "contabilidade oficial". Campo e veterinário **sem acesso financeiro por padrão**.

## Núcleo (candidato ao MVP; escopo do financeiro básico é decisão pendente)
O roadmap põe custo por lote/animal, rateio e fluxo de caixa em F2. Enquanto a decisão não sai, o MVP cobre apenas lançar receita/despesa e registrar venda e compra de animais; o resto abaixo é F2.
- Contas (com saldo inicial); lançamentos de receita, despesa e **transferência**. Data de **competência** e de **pagamento**, forma, categoria, subcategoria, contraparte (funcionário, parceiro, fornecedor, cliente), atividade/fazenda, centro de custo, documento anexo.
- Custo associado a centro de custo, lote ou animal quando conhecido. **Rateio** explicita base, período e parcela não alocada.
- Painel: caixa e custo por cabeça/lote com estado de completude e critérios de inclusão.
- Compra e venda de animais como transação de primeira classe: comprovante imprimível, peso, valor por @/kg/cabeça, rendimento de carcaça.
- Financeiro isolável por propriedade.

## P1
Importação de extrato (OFX) com pré-classificação, revisão e antiduplicidade por referência externa; contas a pagar/receber com alerta de vencimento; DRE; fluxo de caixa; análise de recebimentos e pagamentos.

## Custeio individual
Custo do animal desde nascimento/compra: aquisição + nutrição + sanidade + reprodução + mão de obra + depreciação de pastagem + rateios. Custo por animal produzido, por @ produzida, margem bruta por ha, desembolso por cabeça/mês (= gastos produtivos do mês / rebanho médio, **excluindo aquisição de animais**; benchmark antigo ~R$ 40/cab/mês deve ser parametrizável e indexado).

## Indicadores (declarar fórmula)
Preço médio da @, custo operacional, @/ha/ano, lucro/ha, rentabilidade, margem por lote, simulador de vendas (resultado por kg e por @).

## P2 / fiscal
Integração bancária, NF-e, MDF-e, LCDPR e apuração de IR somente após escopo jurídico/contábil, layout vigente e homologação por UF. Demo/piloto sem certificado do cliente: usar venda gerencial (não fiscal) ou XML fornecido pelo cliente; nunca mockup de homologação nem certificado mantido pela Nexs. Mecânica: certificado digital ICP-Brasil A1/A3 do **próprio cliente** (nunca titular Nexs), chave privada em cofre/keystore criptografado e auditado, nunca em coluna em claro; alerta ao owner ≥30 dias antes de vencer; comunicação com SEFAZ da UF ou SVAN/SVRS, contingência SVC-AN/SVC-RS; LCDPR gerado do razão financeiro, layout fixo da Receita. Capacidades fiscais típicas a especificar: emissão de NF-e e MDF-e, LCDPR, importação automática de NF-e via SEFAZ, conciliação OFX e rateios dinâmicos; BPO financeiro é serviço possível.

### Certificado digital: regra de guarda
- Nunca no MVP: emissão de NF-e/MDF-e é P2/F3. No MVP registra-se a venda no financeiro gerencial e, se houver, importa-se o XML emitido pelo cliente.
- Quando for implementado: **arquivo .pfx cifrado em cofre/keystore (KMS/HSM) fora da tabela de negócio, e a senha guardada separadamente**, nunca junto do arquivo nem na mesma linha/coluna; acesso só pelo serviço de emissão, uso auditado, nunca em log. Alternativa a avaliar: delegar a assinatura/emissão a emissor terceirizado, o que muda a cadeia de responsabilidade e exige aprovação de segurança e jurídico.
- A1 (arquivo) é custodiável em cofre; **A3 (token/cartão físico) não se guarda em SaaS**: exige assinatura local/agente ou delegação, desenho a validar. Vencimento alertado ao owner ≥30 dias antes.
- Arredondamento monetário: 2 casas, half-up, aplicado só na apresentação e no fechamento do lançamento; regra única declarada e testada.

## Retenção de dados financeiros
Lançamentos fiscais: referência de 5 anos (CTN art. 173) — parametrizar por categoria, não prazo único.

## Armadilhas
- Chamar painel gerencial de contabilidade.
- Rateio sem base declarada.
- Custo médio recalculado sem teste.
- Float em dinheiro (usar decimal exato).
