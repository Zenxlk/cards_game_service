# Contribuir

Gracias por el interés en el proyecto. Esto es breve a propósito.

## Antes de mandar un PR

```bash
gofmt -l .        # no debería listar nada
go vet ./...
go test ./...
```

CI corre exactamente esto — si pasa local, pasa en CI.

## Convenciones

- **Código y comentarios en español.** El *por qué* de una decisión no
  obvia se comenta; el *qué* no (el código ya lo dice).
- **Commits en español**, formato [conventional commits](https://www.conventionalcommits.org/es/):
  `feat: agrego X`, `fix: corrijo Y`, `docs: actualizo Z`, `chore(version): bump a 0.2.0`.
- **Un juego nuevo es un paquete nuevo** bajo `games/`, que implementa
  `engine.GameEngine` y se registra con `engine.Register` en su propio
  `init()`. No debería necesitar tocar `room`, `lobby` ni `transport`.
- **Sin rutas absolutas de tu máquina ni datos de un dispositivo/sesión
  concreta** en nada que se commitee (docs, comentarios, ejemplos). Si un
  ejemplo necesita una ruta, usar algo genérico (`/home/usuario/...`).

## Reportar un bug / proponer algo

Abrí un issue. Si es un bug, ayuda mucho incluir cómo reproducirlo — para el
motor de un juego, lo ideal es un test que falle (el motor es una función
pura, no debería hacer falta más que eso).
