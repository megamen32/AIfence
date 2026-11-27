package main

import (
	"agent/internal/sourcewatch"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func registerSourceWatch(a *agentsdk.Agent) {
	watch := sourcewatch.Register(a)
	a.RegisterTool(tool.Typed[sourcewatch.Window, sourcewatch.WatchState]("start_source_watch").
		Description("Enable the owner's recurring weekly observation using approved app-tool bindings. Compare the store metric with search demand, relative search interest or comparable market prices. Runs until stopped.").
		Execute(watch.Start).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[sourcewatch.WatchRef, sourcewatch.WatchRef]("stop_source_watch").
		Description("Stop the owner's observation by its watch ID.").Execute(watch.Stop).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, sourcewatch.SummaryList]("list_source_watches").
		Description("Read the owner's existing background observations and their latest outcomes.").Execute(watch.Summaries).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[sourcewatch.WatchRef, sourcewatch.Review]("review_source_watch").
		Description("Read the completed evidence-checked analysis of one background observation.").Execute(watch.Review).Build(), agentsdk.AccessUser)
	a.AddInstruction(&agentsdk.Instruction{Text: "Для вопросов о включённом фоновом наблюдении сначала прочитай list_source_watches, затем review_source_watch для наблюдения с готовым разбором. Состояние submitted означает, что разбор ещё выполняется. Передай пользователю фактический результат и недостающие сведения; не называй запуск анализа готовой гипотезой. Фоновое наблюдение само не меняет цены и не создаёт задачи во внешнем трекере."})
}
