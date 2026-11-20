/****** Object:  Table [dbo].[orders]    Script Date: 16/11/2026 10:00:00 ******/
SET ANSI_NULLS ON
GO
SET QUOTED_IDENTIFIER ON
GO
CREATE TABLE [dbo].[orders](
	[id] [int] IDENTITY(1,1) NOT NULL,
	[note] [nvarchar](200) NULL CONSTRAINT [DF_orders_note] DEFAULT (N'GO; not a separator'),
	[go] [int] NULL,
 CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id] ASC)
) ON [PRIMARY]
GO
SET IDENTITY_INSERT [dbo].[orders] ON
GO
INSERT [dbo].[orders] ([id], [note], [go]) VALUES (1, N'it''s; fine', 1);
INSERT [dbo].[orders] ([id], [note], [go]) VALUES (2, N'multi
GO
line', 2);
GO
SET IDENTITY_INSERT [dbo].[orders] OFF
GO   -- end of the data
/* a block comment
GO
/* nested
GO */
still a comment
GO */
-- GO in a line comment
CREATE PROCEDURE [dbo].[order_note] @id int
AS
BEGIN
	SET NOCOUNT ON;
	-- GOTO is a word, not GO
	SELECT [note], [go] FROM [dbo].[orders] WHERE [id] = @id;
END
GO
CREATE VIEW [dbo].[GO] AS SELECT "go" = 1
  go
PRINT 'twice'
GO 2
SELECT 'no separator at the end'
