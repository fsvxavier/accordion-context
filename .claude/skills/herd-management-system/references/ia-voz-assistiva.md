# IA assistiva: voz no curral e consulta analítica

Duas pontas complementares; um produto completo cobre as duas com o mesmo vocabulário de domínio:
- **Entrada (voz no curral):** IA na borda, offline; comando de voz → NLP no dispositivo → dado estruturado → *breve revisão* → incorporação. Meta de referência: reduzir em dezenas de por cento o tempo de registro de manejo (validar em piloto). Humano no loop.
- **Consulta (escritório):** linguagem natural por texto ou áudio sobre os dados da fazenda; comparar lotes, analisar indicadores, enviar relatório por mensageria (WhatsApp). Responde citando fonte dos números e janela.
- **Preditivo:** projeção de ganho de peso, parto e risco sanitário; simulação de engorda e de venda por kg e por @, com premissas editáveis.
- **Automação por regras:** alertas e ações disparados por gatilho configurável (ex.: GMD abaixo do esperado, carência a vencer, estoque mínimo).

## Regras de produto
- Voz grava **rascunho**; nada vira registro oficial sem confirmação do operador. Fallback manual sempre.
- Vocabulário fechado de pecuária, validado em ruído de curral e sotaques regionais antes de lançar; medir precisão.
- Rodar na borda para funcionar offline; dados agregados e anonimizados para modelos globais, com consentimento.
- Previsões exibem premissas e incerteza; não são garantia.
- Arquitetura de dados pensada desde cedo para a fase F4 (eventos limpos, unidades, vocabulário); entrega só após dataset e baseline.
- Naming consistente do assistente em todos os materiais.

## Risco
Prometer IA antes de dataset e baseline; gravar automaticamente; alegar ganho de tempo sem medição.
