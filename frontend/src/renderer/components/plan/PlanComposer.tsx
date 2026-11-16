import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Paperclip } from "../app-icons";
import { createPlan } from "../../hooks/usePlans";
import { apiErrorMessage } from "../../lib/api-client";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/**
 * New Task in Plan or Auto mode: a ticket handed to the project orchestrator,
 * which plans it into milestones (Plan) or first decides between a plan and one
 * worker (Auto). There is no session yet, so this is a form rather than the
 * session's chat; it opens the Task's run view once created. The form stays
 * mounted across the two modes, so switching keeps what was typed.
 */
export function PlanComposer({ mode = "plan", projectId, tray }: { mode?: "plan" | "auto"; projectId: string; tray?: ReactNode }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const [title, setTitle] = useState("");
	const [brief, setBrief] = useState("");
	const [ticketRef, setTicketRef] = useState("");
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string>();
	const ready = title.trim().length > 0 && brief.trim().length > 0 && !submitting;

	const submit = async (event?: FormEvent) => {
		event?.preventDefault();
		if (!ready) return;
		setSubmitting(true);
		setError(undefined);
		try {
			const body = {
				title: title.trim(),
				brief,
				...(ticketRef.trim() ? { ticketRef: ticketRef.trim() } : {}),
				...(mode === "auto" ? { mode } : {}),
			};
			const plan = await createPlan(projectId, body);
			void navigate({ to: "/projects/$projectId/plans/$planId", params: { projectId, planId: plan.id } });
		} catch (failure) {
			setError(apiErrorMessage(failure, t("newTask.plan.failed")));
			setSubmitting(false);
		}
	};
	const submitOnModEnter = (event: KeyboardEvent<HTMLElement>) => {
		if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			void submit();
		}
	};

	return (
		<div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-y-auto px-4 pb-16">
			<form className="flex w-full max-w-[680px] flex-col gap-4" onSubmit={(event) => void submit(event)} onKeyDown={submitOnModEnter}>
				<h1 className="mb-1 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl">
					{mode === "auto" ? t("newTask.plan.headingAuto") : t("newTask.plan.heading")}
				</h1>
				<div className="flex flex-col gap-2">
					<Label htmlFor="plan-title">{t("newTask.plan.title")}</Label>
					<Input
						id="plan-title"
						value={title}
						placeholder={t("newTask.plan.titlePlaceholder")}
						onChange={(event) => setTitle(event.target.value)}
						disabled={submitting}
						autoFocus
					/>
				</div>
				<div className="flex flex-col gap-2">
					<Label htmlFor="plan-brief">{t("newTask.plan.brief")}</Label>
					<textarea
						id="plan-brief"
						value={brief}
						rows={8}
						placeholder={t("newTask.plan.briefPlaceholder")}
						onChange={(event) => setBrief(event.target.value)}
						disabled={submitting}
						className={cn(
							"min-h-32 w-full min-w-0 resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 text-sm text-foreground outline-none",
							"placeholder:text-muted-foreground focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
						)}
					/>
				</div>
				<div className="flex flex-col gap-2">
					<Label htmlFor="plan-ticket">{t("newTask.plan.ticket")}</Label>
					<Input
						id="plan-ticket"
						value={ticketRef}
						className="font-mono"
						placeholder={t("newTask.plan.ticketPlaceholder")}
						onChange={(event) => setTicketRef(event.target.value)}
						disabled={submitting}
					/>
				</div>
				<div className="flex items-center gap-2">
					{tray}
					<Tooltip>
						<TooltipTrigger asChild>
							{/* A disabled button gets no pointer events; the span carries the tooltip. */}
							<span tabIndex={0} className="inline-flex">
								<Button type="button" variant="quiet" size="icon-sm" disabled aria-label={t("newTask.plan.attach")}>
									<Paperclip className="size-icon-sm" aria-hidden />
								</Button>
							</span>
						</TooltipTrigger>
						<TooltipContent>{t("newTask.plan.attachUnsupported")}</TooltipContent>
					</Tooltip>
					<div className="flex-1" />
					<Button type="submit" variant="primary" size="sm" disabled={!ready}>
						{mode === "auto"
							? submitting ? t("newTask.plan.submittingAuto") : t("newTask.plan.submitAuto")
							: submitting ? t("newTask.plan.submitting") : t("newTask.plan.submit")}
					</Button>
				</div>
				{mode === "auto" ? <p className="px-1 text-xs text-muted-foreground">{t("newTask.plan.autoHint")}</p> : null}
				{error ? <p role="alert" className="px-1 text-xs text-destructive">{error}</p> : null}
			</form>
		</div>
	);
}
