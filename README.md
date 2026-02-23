# WhatsMeow Basileia

Ferramenta de mensageria robusta baseada em `whatsmeow` (Go).

## Estrutura do Projeto

- **cmd/server**: Ponto de entrada da aplicação.
- **internal/core**: Domínio e interfaces (Clean Architecture).
- **internal/infrastructure**: Implementações (WhatsApp, Database).
- **internal/service**: Lógica de negócio e casos de uso.
- **pkg**: Utilitários compartilhados (Logger, Config).

## Pré-requisitos

- Go 1.23+
- Docker (opcional, para Banco de Dados)
- GCC (necessário para SQLite se não usar CGO_ENABLED=0 ou build tags adequadas)

## Como Rodar

1.  **Dependências**:
    ```bash
    go mod tidy
    ```

2.  **Configuração**:
    A aplicação usa variáveis de ambiente. O padrão usa SQLite local para facilitar o teste.
    
    Para usar PostgreSQL:
    ```bash
    export DB_DIALECT=postgres
    export DB_ADDRESS="postgres://user:pass@localhost/dbname?sslmode=disable"
    ```

3.  **Executar**:
    ```bash
    go run cmd/server/main.go
    ```

4.  **Usar**:
    Abra `http://localhost:8080/` no navegador para conectar via QR Code.

## API

- `POST /device`: Cria nova sessão.
- `GET /device/{id}/qr`: Stream de QR Code (SSE).
- `GET /device/{id}/status`: Status da conexão.

## Funcionalidades Implementadas

- **Conexão**: Gerenciamento completo de ciclo de vida.
- **Múltiplas Contas**: Suporte a múltiplos dispositivos simultâneos.
- **Fluent API**: Envio fácil de mensagens.
- **Botões**: Suporte experimental a mensagens de botão.
- **Frontend**: Interface web simples para conexão.
