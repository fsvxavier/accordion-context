# Personas e jornadas (hipóteses a validar com pesquisa)

## Perfis
| Perfil | Necessidade | Limite de acesso |
|---|---|---|
| Proprietário/administrador | Visão por fazenda/lote, resultado, risco, auditoria | Só propriedades próprias/autorizadas |
| Gerente/capataz | Tarefas, pendências, sincronização, alertas | Permissões explícitas para caixa, documentos, exclusões |
| Vaqueiro/trabalhador de campo | Poucos passos, offline, leitura confiável, feedback claro | Só tarefas/dados da atividade e fazenda |
| Veterinário | Histórico por animal/lote, tratamentos, carência, diagnóstico | Delegado por fazenda e tempo, sem financeiro |
| Zootecnista/consultor | Indicadores comparáveis, carteira autorizada | Consentimento explícito; separação entre clientes |
| Administrativo/contador | Categorias, conciliação, exportação | Dados financeiros sob concessão específica |
Contexto real: baixa alfabetização digital, sol, luvas, rede ruim, hardware modesto; produtor misto com pouca identificação individual.

## Jornadas críticas
- **J1 Primeira importação:** cadastra fazenda e lotes, importa planilha ou inventário individual/agregado, revisa duplicidades, confirma totais. Relatório de válidas/rejeitadas/ambíguas; nunca converte saldo agregado em animais artificiais. Aceite: diferença origem × importado visível e confirmada.
- **J2 Manejo offline:** gerente baixa lote/tarefa; trabalhador lê brinco, pesa, maneja, vê confirmação local; fila durável; reconexão com retry idempotente; aprovação opcional não bloqueia offline. Aceite: queda de rede/reinício não perde evento; brinco desconhecido/duplicado → resolução; origem/dispositivo e correção preservados.
- **J3 Pesagem e indicador:** escolhe pesagens-base, confere data/unidade, vê GMD com fórmula, intervalo e exclusões; correção não apaga original. Aceite: memória de cálculo reproduzível, mesmo resultado em relatório/exportação.
- **J4 Reprodução e parto:** estação, grupo, método/touro/sêmen/protocolo; diagnóstico; sistema sugere próxima ação/data com parâmetros visíveis; parto cria vínculo mãe/cria e paternidade com grau de certeza.
- **J5 Sanidade e carência:** registra produto/lote/dose; estoque baixa uma vez; carência versionada alerta ao selecionar para venda/abate.
- **J6 Confinamento e trato:** nutricionista configura dieta e lote; equipe registra trato e leitura de cocho offline; gestor compara planejado × realizado, consumo e custo.
- **J7 Fechamento:** owner revisa receita/despesa, compras, venda, custos não alocados e métricas; separa caixa gerencial de escrituração fiscal e lista dados incompletos.
- **J8 Consultor multi-fazenda:** convite por fazenda, troca de contexto, sem registros cruzados; revogação expira cache local.
- **Transversal erro/suporte:** evento afetado, estado, ação segura, risco de duplicar, canal de suporte com request_id.
