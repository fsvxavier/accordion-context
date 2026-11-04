# Organização, papéis, acesso e multi-fazenda

## Hierarquia
Conta/organização → fazendas (N) → setores/áreas/instalações → lotes → animais. Fuso horário, unidades e parâmetros zootécnicos por fazenda. Estoque e financeiro compartilhados ou isolados por fazenda.

## Papéis-base
owner (proprietário), gestor, veterinário/técnico, trabalhador de campo. Permissões granulares derivam do papel **e** de delegação explícita; liberar/bloquear funcionalidade por funcionário (ex.: estoquista só vê estoque). Campo e veterinário sem financeiro por padrão. Delegação por fazenda e por tempo; ações atribuídas a ator, horário, dispositivo e escopo.

## Consultor / técnico multi-fazenda
Convite de cada fazenda; alterna entre fazendas explicitamente autorizadas; **não vê dados cruzados**; sem financeiro por padrão. Owner revoga; app expira cache local conforme política. Planos de consultor por nº de propriedades/animais e app de técnico de reprodução cobrado por créditos são canais de aquisição a considerar.

### Convite, delegação e revogação (padrões a adotar)
- Convite **por fazenda**, emitido pelo owner daquela fazenda, com expiração; aceite cria vínculo consultor↔fazenda com escopo (módulos permitidos: por padrão rebanho, pesagem, reprodução, sanidade e estoque da fazenda; **sem financeiro**) e prazo opcional. A delegação é por fazenda e por módulo, nunca global.
- Revogação: efeito imediato no servidor; as outras fazendas do consultor seguem intactas. O app descarta o cache local dessa fazenda no próximo contato com a rede, ou ao fim de um TTL curto de cache configurado pela política; dados offline do consultor não somem antes disso, então o TTL é decisão de segurança a fixar com o cliente.
- Eventos offline pendentes do consultor na hora da revogação: são **rejeitados com motivo "acesso revogado"** e ficam visíveis ao owner como não aceitos; nunca aplicados silenciosamente nem descartados sem rastro.
- Autoria: registros e auditoria feitos pelo consultor permanecem atribuídos a ele (identificador do ator preservado); retenção e anonimização seguem a política por categoria de dado.

## Isolamento
- Row-Level Security no PostgreSQL como fronteira (não filtro de aplicação), **fail-closed**, `FORCE ROW LEVEL SECURITY` em tabela de tenant; contexto de sessão por requisição (ator, organizações, fazendas, papel, flag owner).
- Materialized views sempre atrás de view wrapper que reaplica filtro de contexto.
- Teste cross-tenant/cross-farm no CI com RLS ligada, papéis independentes, views e jobs. Nunca remover controle para deixar teste verde.
- Autenticação forte para ações administrativas; segredos rotacionáveis; acesso privilegiado auditado.
- Dados pessoais minimizados e fora de logs; exportação só de registros autorizados.

## Consolidado
Owner vê resumo consolidado das fazendas autorizadas com drill-down e origem por fazenda (P1).

## Modos de complexidade
Toggle Simplificado/Personalizável por feature flag de tenant (progressive disclosure) — não pode esconder controles de segurança nem alterar regra de domínio.

## Auditoria
Trilha híbrida (triggers + contexto de aplicação) em toda tabela de domínio; correções ligam antes/depois, motivo e ator; eventos e auditoria sem soft delete indiscriminado.
