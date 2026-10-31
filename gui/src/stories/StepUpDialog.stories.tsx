import { useState } from 'react';
import type { Story } from '@ladle/react';
import { Button } from '@moduleforge/core-gui';
import { StepUpDialog } from '../components/step-up-dialog';
import { api } from '../lib/api';
import { SignedInStory, STORY_PERSONAL, storyError, storyStepUpRequired } from './story-support';

const PROFILES = [STORY_PERSONAL];

function tokenReply() {
  return {
    access_token: 'story-fresh',
    token_type: 'Bearer',
    expires_in: 3600,
    refresh_token: null,
    session: {
      uuid: 'story-session',
      acr: 2,
      amr: ['pwd'],
      auth_time: new Date().toISOString(),
      impersonating: false,
    },
    profile: STORY_PERSONAL,
    selection_required: false,
    profiles: PROFILES,
  };
}

/** Runs a gated call: the first attempt answers the step-up 409, the retry succeeds. */
function Trigger() {
  const [result, setResult] = useState<string>('Not run yet.');
  return (
    <div className="flex flex-col gap-2 p-4">
      <Button
        type="button"
        onClick={() => {
          setResult('Running...');
          api.me.sessions
            .list()
            .then(() => setResult('The request succeeded after the step-up.'))
            .catch(() => setResult('The request was abandoned.'));
        }}
      >
        Run a gated request
      </Button>
      <p role="status" className="text-sm">
        {result}
      </p>
    </div>
  );
}

export const PasswordOrEmailCode: Story = () => {
  let unlocked = false;
  return (
    <SignedInStory
      profiles={PROFILES}
      routes={{
        'GET /v1/me/sessions': () => (unlocked ? { sessions: [] } : storyStepUpRequired()),
        'POST /v1/session/step-up/password': () => {
          unlocked = true;
          return tokenReply();
        },
        'POST /v1/session/step-up/email-code/request': () => undefined,
        'POST /v1/session/step-up/email-code/verify': () => {
          unlocked = true;
          return tokenReply();
        },
      }}
    >
      <StepUpDialog providers={[]} />
      <Trigger />
    </SignedInStory>
  );
};

export const WithProviderChoice: Story = () => (
  <SignedInStory
    profiles={PROFILES}
    routes={{ 'GET /v1/me/sessions': () => storyStepUpRequired() }}
  >
    <StepUpDialog providers={[{ id: 'google', display_name: 'Google' }]} />
    <Trigger />
  </SignedInStory>
);

/** A wrong password is the same 409 action; the prompt stays open with a message. */
export const WrongPassword: Story = () => (
  <SignedInStory
    profiles={PROFILES}
    routes={{
      'GET /v1/me/sessions': () => storyStepUpRequired(),
      'POST /v1/session/step-up/password': () => storyStepUpRequired(),
      'POST /v1/session/step-up/email-code/request': () =>
        storyError(500, 'internal', 'Could not send the code.'),
    }}
  >
    <StepUpDialog providers={[]} />
    <Trigger />
  </SignedInStory>
);
