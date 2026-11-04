# Hardware: balanças, bastões e leitores RFID

## Princípio de comunicação
Nunca publicar "compatível" por marca, protocolo anunciado ou funcionamento em um único SO. Matriz pública registra o teste: modelo, firmware, plataforma, app/versão, ID, peso, offline, Android, iOS, limitações, data. Suporte parcial explícito. Android e iOS só declarados após passar nos dois.

## Níveis de integração
| Nível | Meio | Uso |
|---|---|---|
| 0 Manual | digitação/importação | fallback sempre disponível |
| 1 HID | dispositivo "digita" o ID (teclado virtual) | acelera leitura de brinco; não lê peso nem estado; testar foco, separadores, duplicidade |
| 2 Protocolo | BLE/serial/SDK do fabricante | ID/peso/estado, reconexão, erros; homologar modelos prioritários com equipamento físico |
| 3 Autoconfiguração | pareamento guiado, parser autorizado | só após segurança do parser, UX e suporte comprovados (usuário cadastra/homologa equipamento) |

## Candidatos (não suportados até prova)
Tru-Test/Datamars: Eziweigh7i, XR5000, S3, S2, SRS1, SRS2/SRS2i, XRS2/XRS2i, painel XRP2/XRP2i. Gallagher: W-0, W1, W810, HR3. Allflex: AWR250, AWR300, RS420/RS420+. Coimma KM 3 Plus. Toledo MGR 4000. Açores ACR 1500/PLUS, BPB085, HD Easy. Pesenti, Inbalance, Valfran, Alltags, Prix, Animalltag: pedir modelo e protocolo. Comportamento difere por modelo (ex.: um envia peso automático, outro exige apertar botão).

## Casos de teste por par dispositivo/plataforma
Parear e reconectar após desligar, perder sinal, bloquear tela, reiniciar. Brinco desconhecido, repetido, truncado, duplicado, dois IDs por animal. Pesagem zero, negativa, sobrecarga, estabilização, repetição. Unidade, casas, separador decimal, terminador, timestamp, sequência ID→peso. Fila offline, lote grande, evento repetido, troca rápida, cancelar, correção. Latência, falha, bateria, alternar apps. Android e iOS em versões suportadas, em curral real.

## Gate de homologação
1. Modelo físico + documentação/SDK autorizados. 2. Contrato de protocolo e dados. 3. Implementação isolada (sandbox) com timeout, reconexão e buffer, fora da lógica de domínio. 4. Casos acima em dispositivos alvo e offline. 5. Usuários de campo em operação piloto. 6. Registrar versão, data, firmware, resultados, limitações, fallback. 7. Publicar só após aceite de produto/QA; monitorar incidentes e descontinuar versão sem suporte.

## Riscos
- Bluetooth com hardware de curral é o maior risco de engenharia (a mesma integração pode exigir várias correções ao longo de anos): camada de abstração de driver.
- Android: fragmentação, permissões de Bluetooth/localização, economia de bateria que mata serviço. iOS: sem SPP clássico sem MFi, background restrito — automação RFID/balança em iOS costuma ser limitada; só declarar suporte se realmente testado.
- Homologação sob demanda (cliente envia aparelho) e auto-homologação pelo usuário são práticas úteis.
- Bastão separado de balança: funcionam de forma independente.
