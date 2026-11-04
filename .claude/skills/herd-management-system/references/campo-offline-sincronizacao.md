# Campo, manejo, tarefas e sincronização offline

## Fluxo de curral
Gerente baixa lista de trabalho por fazenda/lote com data de sincronização visível → vaqueiro lê brinco ou seleciona animal → registra pesagem e manejos (vários na mesma lida) → confirmação local → eventos persistidos em fila durável → ao reconectar, envio, retry e resultado aceito/rejeitado/conflito. Cadastro de animal novo durante o manejo; apartação planejada; troca rápida de lote; registro por GPS do local. Meta de usabilidade de referência: pesagem em ~12 s/animal.

## Construtor de manejos
Manejo como **composição de ações** (9+: pesar, vacinar, medicar, apartar, brincar, diagnosticar, mover…) arrastadas, nomeadas, versionadas e executáveis offline; biblioteca/template compartilhável. Template novo não altera eventos históricos. Calendário de manejo; execução integrada a estoque (saída automática de medicamento/dose). Ordem do manejo escolhida pelo usuário.

## Tarefas de campo ("Processo Mangueiro")
Gestor designa atividade (movimentar, vacinar animal ou lote) a pessoa/equipe, com lote, janela, instrução e status; peão executa interface simplificada; trilha de **quem fez o quê**; validação previne erro de registro. Anexos sujeitos a quota e retenção. Navegação guiada para baixa instrução. App do peão sem custo por usuário (usuários ilimitados por assinatura) remove atrito de adoção.

## Fluxo de aprovação (dupla custódia)
Dados coletados entram como **solicitações pendentes**; gestor revisa, aprova ou reprova; só aprovado entra na base oficial. Configurável por fazenda/tipo de manejo; **nunca bloqueia o uso offline**. Estados explícitos (máquina de estados): rascunho local → enviado → pendente → aprovado | reprovado | conflito | excluído-antes-do-envio. Cópia do registro, ator e razão preservados. Aprovação agrupada (cadastros, "aprovar todos os manejos da solicitação"). Reprovado não reaparece. Exclusão local pós-aprovação exige autorização. Estados intermediários são onde mais surgem bugs: modelar com cuidado.

## Sincronização
- Cada evento recebe **identidade/idempotency key no cliente** (UUID v7 + device); servidor deduplica. Reenvio não duplica efeito nem baixa de estoque.
- Login online ao menos uma vez; depois offline indefinido. Persistência local sobrevive a fechar/reiniciar; monitorar espaço e backlog.
- Fila visível: pendente, enviado, aceito, rejeitado, conflito, com retry e explicação; **falha nunca some**.
- Conflito: sem resolução automática insegura; **timestamp de dispositivo não é fonte infalível**. Guardar hora de negócio, hora de recepção, fuso e origem; validar plausibilidade e sinalizar relógio divergente; ordenar por contador lógico, não por hora de recepção. Conflito de cadastro/identidade vai a resolução humana. Fatos distintos (pesagens, tratamentos) somam; campos mutáveis (lote, categoria, situação) por regra de domínio.
- Prever ressincronização manual e sincronização pontual de animal: divergência ocorre na prática.
- Operações em lote: resumo antes de confirmar e resultado depois de sincronizar.
- Bateria e sincronização testadas em hardware/SO reais.

## Entrada por voz / IA assistiva
Ver ia-voz-assistiva. Sempre com revisão humana e fallback manual.

## UX de campo
Legível ao sol, botões grandes, uso com luvas, feedback sonoro/vibratório, mínimo de telas, confirmação para evitar leitura atribuída ao animal errado, pt-BR com unidades locais, hardware modesto (não exigir PC nem navegador pesado). O mobile deve **editar** a ficha do animal, não apenas coletar. iOS com hardware de curral só após teste físico.

## Erro e suporte
Todo erro informa: evento afetado, estado, ação segura, se a ação pode duplicar efeito, como chamar suporte; suporte recebe request_id/event ID sem credenciais nem extrato clínico/financeiro.
